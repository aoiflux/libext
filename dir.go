package libext

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	extDirentTypeDirectory = 2
)

func (fs *FS) ListDir(inodeNum uint32) ([]DirEntry, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	if !inode.IsDirectory {
		return nil, ErrNotDirectory
	}
	data, err := fs.readInodeData(inode)
	if err != nil {
		return nil, err
	}
	// An inline directory uses a different layout: no "." or ".." records, and
	// the first four bytes are the parent inode rather than a record header.
	if inode.HasInline {
		return fs.inlineDirEntries(inode, data)
	}
	return fs.parseDirEntries(data)
}

// dirEntryAlign is the on-disk alignment of a directory record. It is the step
// used to resynchronise after a damaged record.
const dirEntryAlign = 4

// parseDirEntries walks a directory data stream.
//
// A malformed record no longer discards the whole directory: entries parsed
// before the fault are always returned. In permissive mode the walk
// resynchronises to the next 4-byte boundary and keeps going, which recovers the
// records that follow damage in the middle of a block.
func (fs *FS) parseDirEntries(data []byte) ([]DirEntry, error) {
	entries := make([]DirEntry, 0, 32)
	hasFileType := (fs.sb.FeatureIncompat & featureIncompatFileType) != 0

	for off := 0; off+8 <= len(data); {
		entry, recLen, err := parseDirEntryAt(data, off, hasFileType)
		if err != nil {
			// A zero record length is how a truncated tail presents; treat it as
			// the end of the stream rather than as damage.
			if errors.Is(err, errDirEntryEnd) && !fs.opts.Permissive {
				return entries, nil
			}
			if !fs.opts.Permissive {
				return entries, err
			}
			fs.warn(WarnDegradedRead, "", fmt.Sprintf(
				"directory record at offset %d is malformed (%v); resynchronising", off, err))
			off += dirEntryAlign
			continue
		}

		if entry.Inode != 0 {
			entries = append(entries, entry)
		}
		off += recLen
	}
	return entries, nil
}

// errDirEntryEnd marks a record length of zero, which terminates a stream.
var errDirEntryEnd = errors.New("zero directory record length")

// parseDirEntryAt decodes the record at off and returns its on-disk length.
func parseDirEntryAt(data []byte, off int, hasFileType bool) (DirEntry, int, error) {
	inode := le32(data, off)
	recLen := int(le16(data, off+4))
	if recLen == 0 {
		return DirEntry{}, 0, errDirEntryEnd
	}
	if recLen < 8 {
		return DirEntry{}, 0, fmt.Errorf("%w: record length %d below minimum", ErrUnsupportedLayout, recLen)
	}
	if recLen%dirEntryAlign != 0 {
		return DirEntry{}, 0, fmt.Errorf("%w: record length %d is not %d-byte aligned", ErrUnsupportedLayout, recLen, dirEntryAlign)
	}
	if off+recLen > len(data) {
		return DirEntry{}, 0, fmt.Errorf("%w: record at %d overruns %d bytes of directory data", ErrUnsupportedLayout, off, len(data))
	}

	nameLen := uint8(data[off+6])
	fileType := uint8(data[off+7])
	if !hasFileType {
		nameLen16 := le16(data, off+6)
		if nameLen16 > 255 {
			return DirEntry{}, 0, fmt.Errorf("%w: name length %d exceeds 255", ErrUnsupportedLayout, nameLen16)
		}
		nameLen = uint8(nameLen16)
		fileType = 0
	}
	if int(nameLen) > recLen-8 {
		return DirEntry{}, 0, fmt.Errorf("%w: name length %d does not fit record length %d", ErrUnsupportedLayout, nameLen, recLen)
	}

	return DirEntry{
		Inode:       inode,
		RecLen:      uint16(recLen),
		NameLen:     nameLen,
		FileType:    fileType,
		Name:        string(data[off+8 : off+8+int(nameLen)]),
		IsDirectory: hasFileType && fileType == extDirentTypeDirectory,
	}, recLen, nil
}

// DirOptions controls how much per-entry detail a directory listing gathers.
// The zero value matches ListDir: names and types only, one read for the whole
// directory.
type DirOptions struct {
	// WithInodeMetadata fills Times, Mode, UID, GID, Size, IsDirectory and
	// Deleted by reading each entry's inode. Costs one inode read per entry.
	WithInodeMetadata bool

	// IncludeDotEntries keeps "." and ".." in the result.
	IncludeDotEntries bool
}

// ListDirEx lists a directory with per-entry detail controlled by opts.
func (fs *FS) ListDirEx(inodeNum uint32, opts DirOptions) ([]DirEntry, error) {
	entries, err := fs.ListDir(inodeNum)
	if err != nil {
		return entries, err
	}
	return fs.decorateEntries(entries, opts), nil
}

// ReadDirEx reads the directory with per-entry detail controlled by opts.
func (f *File) ReadDirEx(opts DirOptions) ([]DirEntry, error) {
	if !f.inode.IsDirectory {
		return nil, ErrNotDirectory
	}
	return f.volume.ListDirEx(f.inode.Number, opts)
}

// decorateEntries applies DirOptions to a raw listing.
func (fs *FS) decorateEntries(entries []DirEntry, opts DirOptions) []DirEntry {
	out := entries[:0]
	for _, e := range entries {
		if !opts.IncludeDotEntries && (e.Name == "." || e.Name == "..") {
			continue
		}
		if opts.WithInodeMetadata {
			fs.fillEntryFromInode(&e)
		}
		out = append(out, e)
	}
	return out
}

// fillEntryFromInode copies inode-level detail onto a directory entry. Failures
// are silent: an entry whose inode cannot be read is still a real name that was
// present in the directory, and dropping it would lose evidence.
func (fs *FS) fillEntryFromInode(e *DirEntry) {
	inode, err := fs.ReadInode(e.Inode)
	if err != nil {
		return
	}
	e.IsDirectory = inode.IsDirectory
	e.Size = inode.Size
	e.Times = inode.Timestamps()
	e.Mode = inode.Mode
	e.UID = inode.UID
	e.GID = inode.GID
	e.Deleted = inode.Deleted()
}

func (fs *FS) LookupPath(p string) (DirEntry, error) {
	clean := path.Clean("/" + strings.TrimSpace(p))
	if clean == "/" {
		return DirEntry{Inode: 2, Name: "/", FileType: 2}, nil
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	cur := uint32(2)
	var found DirEntry

	for _, part := range parts {
		entries, err := fs.ListDir(cur)
		if err != nil {
			return DirEntry{}, err
		}
		matched := false
		for _, e := range entries {
			if e.Name == part {
				cur = e.Inode
				found = e
				matched = true
				break
			}
		}
		if !matched {
			return DirEntry{}, fmt.Errorf("%w: %s", ErrPathNotFound, p)
		}
	}
	return found, nil
}

func (fs *FS) WalkDir(startInode uint32, fn func(p string, entry DirEntry) error) error {
	if fn == nil {
		return errors.New("walk callback is nil")
	}
	root, err := fs.ReadInode(startInode)
	if err != nil {
		return err
	}
	if !root.IsDirectory {
		return ErrNotDirectory
	}
	return fs.walkDirRecursive(startInode, "/", fn)
}

func (fs *FS) walkDirRecursive(inodeNum uint32, curPath string, fn func(p string, entry DirEntry) error) error {
	entries, err := fs.ListDir(inodeNum)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		nextPath := path.Join(curPath, e.Name)
		if err := fn(nextPath, e); err != nil {
			return err
		}
		child, err := fs.ReadInode(e.Inode)
		if err != nil {
			continue
		}
		if child.IsDirectory {
			if err := fs.walkDirRecursive(e.Inode, nextPath, fn); err != nil {
				return err
			}
		}
	}
	return nil
}
