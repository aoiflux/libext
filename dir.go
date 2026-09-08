package libext

import (
	"context"
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
	//
	// Both paths stamp through the error rather than around it: a malformed record
	// still yields the entries parsed before the fault, and those entries were read
	// from this directory whether or not the tail of it decoded.
	if inode.HasInline {
		entries, err := fs.inlineDirEntries(inode, data)
		return stampParent(entries, inodeNum), err
	}
	entries, err := fs.parseDirEntries(data)
	return stampParent(entries, inodeNum), err
}

// stampParent records the directory each record was read from.
//
// It is applied at the listing call rather than inside parseDirEntries because
// the parser sees only bytes: the containing inode number is knowledge the
// caller has and the record does not.
func stampParent(entries []DirEntry, parent uint32) []DirEntry {
	for i := range entries {
		entries[i].ParentInode = parent
	}
	return entries
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
	// WithInodeMetadata fills Times, Mode, UID, GID, Size, Generation,
	// IsDirectory and Deleted by reading each entry's inode. Costs one inode read
	// per entry.
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

// applyInode copies inode-level detail onto a directory entry.
//
// It is the single statement of which fields an inode contributes, so the
// listing calls and the walk cannot drift about it. Note that IsDirectory is
// taken from the inode's mode rather than from the record's file type: the two
// agree on a modern filesystem, but a filesystem without the FILETYPE feature
// records no type in the directory at all, and the mode is then the only
// truthful source.
func applyInode(e *DirEntry, inode Inode) {
	e.IsDirectory = inode.IsDirectory
	e.Size = inode.Size
	e.Times = inode.Timestamps()
	e.Mode = inode.Mode
	e.UID = inode.UID
	e.GID = inode.GID
	e.Generation = inode.Generation
	e.Deleted = inode.Deleted()
}

// fillEntryFromInode copies inode-level detail onto a directory entry. Failures
// are silent: an entry whose inode cannot be read is still a real name that was
// present in the directory, and dropping it would lose evidence.
func (fs *FS) fillEntryFromInode(e *DirEntry) {
	inode, err := fs.ReadInode(e.Inode)
	if err != nil {
		return
	}
	applyInode(e, inode)
}

func (fs *FS) LookupPath(p string) (DirEntry, error) {
	clean := path.Clean("/" + strings.TrimSpace(p))
	if clean == "/" {
		// The root is its own parent: its ".." record points back at itself.
		//
		// IsDirectory is stated alongside FileType because the root is a directory
		// by definition. Leaving it false while FileType said 2 made this the one
		// entry in the package where a caller reading the two fields got opposite
		// answers to the same question.
		return DirEntry{
			Inode:       RootInode,
			Name:        "/",
			FileType:    2,
			IsDirectory: true,
			ParentInode: RootInode,
		}, nil
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

// WalkDir visits every entry beneath startInode, depth first.
//
// Paths are built relative to startInode and presented with a leading "/", so
// walking a subdirectory yields subtree-relative paths rather than absolute
// ones. Use DirEntry.ParentInode with PathFor when the absolute path is wanted.
//
// The entries handed to the callback carry their inode metadata - Times, Mode,
// UID, GID, Size, Generation and Deleted - because the walk reads each child's
// inode anyway to decide whether to descend. The callback pays nothing for it.
// A callback needing more of the inode than that should use WalkDirWithInode,
// which hands over the whole parsed inode rather than a second read of it.
//
// The "." and ".." records are never reported. A directory reachable more than
// once is walked once and warned about rather than followed again.
//
// A non-nil error from the callback ends the walk and is returned unchanged.
func (fs *FS) WalkDir(startInode uint32, fn func(p string, entry DirEntry) error) error {
	return fs.WalkDirContext(context.Background(), startInode, fn)
}

// WalkDirContext is WalkDir with cancellation.
//
// A walk of a large tree is slow enough to want interrupting; cancelling the
// context stops it at the next checkpoint and returns the context's error.
// Entries already handed to the callback are not retracted.
func (fs *FS) WalkDirContext(ctx context.Context, startInode uint32, fn func(p string, entry DirEntry) error) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if fn == nil {
		return errors.New("walk callback is nil")
	}
	return fs.walkDir(ctx, startInode, func(p string, e DirEntry, _ Inode) error {
		return fn(p, e)
	})
}

// WalkDirWithInode is WalkDir with each child's parsed inode handed to the
// callback alongside the entry.
//
// The walk reads that inode either way - it needs IsDirectory to decide whether
// to descend - so a caller wanting more of it than DirEntry carries (the link
// count, the extent tree, the flags, the raw block map) gets it without a
// second read. Building a report over a large tree halves its inode reads this
// way; that is what this shape exists for.
//
// When an entry's inode cannot be read the callback still runs, receiving the
// zero Inode - recognisable by Number == 0, since inode numbering starts at 1 -
// and an entry carrying only what the directory record itself said. A record
// naming an unreadable inode is evidence in its own right, and dropping it
// would lose that.
//
// Everything else is WalkDir's: the same relative paths, the same skipped "."
// and ".." records, the same cycle guard and depth cap, the same handling of a
// callback error.
func (fs *FS) WalkDirWithInode(startInode uint32, fn func(p string, entry DirEntry, inode Inode) error) error {
	return fs.WalkDirWithInodeContext(context.Background(), startInode, fn)
}

// WalkDirWithInodeContext is WalkDirWithInode with cancellation, on the same
// terms as WalkDirContext.
func (fs *FS) WalkDirWithInodeContext(ctx context.Context, startInode uint32, fn func(p string, entry DirEntry, inode Inode) error) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if fn == nil {
		return errors.New("walk callback is nil")
	}
	return fs.walkDir(ctx, startInode, fn)
}

// walkDir is the one traversal. Both exported callback shapes go through it, so
// the cycle guard, the depth cap, the pacing of cancellation checks and the
// order entries are reported in cannot drift apart between them.
func (fs *FS) walkDir(ctx context.Context, startInode uint32, fn func(p string, entry DirEntry, inode Inode) error) error {
	root, err := fs.ReadInode(startInode)
	if err != nil {
		return err
	}
	if !root.IsDirectory {
		return ErrNotDirectory
	}
	w := &dirWalk{
		fs:  fs,
		ctx: ctx,
		fn:  fn,
		// The start is already entered, so a record pointing back at it is a
		// cycle rather than a first arrival.
		seen: map[uint32]bool{startInode: true},
	}
	return w.descend(startInode, "/", 0)
}

// dirWalk carries the state one walk needs: the cancellation pacing counter and
// the set of directories already entered.
//
// Both are walk-wide rather than per-directory. A per-directory counter would
// almost never reach cancellationCheckInterval - most directories hold a
// handful of entries - so a walk over a million small directories would never
// observe a cancellation at all.
type dirWalk struct {
	fs      *FS
	ctx     context.Context
	fn      func(p string, entry DirEntry, inode Inode) error
	visited int
	seen    map[uint32]bool
}

func (w *dirWalk) descend(inodeNum uint32, curPath string, depth int) error {
	if depth > maxWalkDepth {
		w.fs.warn(WarnDegradedRead, "", fmt.Sprintf(
			"directory nesting at %s exceeds %d levels; not descending further", curPath, maxWalkDepth))
		return nil
	}
	entries, err := w.fs.ListDir(inodeNum)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}

		// Cancellation is checked per batch of entries rather than per entry: the
		// check is cheap but not free. The count runs across the whole walk
		// rather than resetting at each directory, so a tree of small
		// directories stays interruptible, and it is tested before being
		// incremented so a context already cancelled is caught on the first
		// entry rather than the thousandth.
		if w.visited%cancellationCheckInterval == 0 {
			if err := w.ctx.Err(); err != nil {
				return err
			}
		}
		w.visited++

		nextPath := path.Join(curPath, e.Name)

		// The child's inode is read before the callback rather than after,
		// because the walk reads it either way: it needs IsDirectory to decide
		// whether to descend. Reading it first lets the entry the callback
		// receives carry the metadata that read already produced, at no extra
		// cost. A read that fails still yields the callback the record's name,
		// which is real evidence whether or not the inode behind it survives.
		child, cerr := w.fs.ReadInode(e.Inode)
		if cerr != nil {
			// Zeroed explicitly rather than trusted to be zero, because the
			// callback contract is that Number == 0 marks an unread inode and a
			// half-populated one would break it.
			child = Inode{}
		} else {
			applyInode(&e, child)
		}

		if err := w.fn(nextPath, e, child); err != nil {
			return err
		}
		if cerr != nil || !child.IsDirectory {
			continue
		}

		// A directory has exactly one parent in a valid ext filesystem, so a
		// second arrival means a crafted or damaged tree. Without this the walk
		// recurses until the stack is gone.
		if w.seen[e.Inode] {
			w.fs.warn(WarnDegradedRead, "", fmt.Sprintf(
				"directory inode %d is reachable more than once (at %s); not descending again",
				e.Inode, nextPath))
			continue
		}
		w.seen[e.Inode] = true
		if err := w.descend(e.Inode, nextPath, depth+1); err != nil {
			return err
		}
	}
	return nil
}
