package libext

import (
	"fmt"
	"io"
)

// File represents an opened EXT inode with file-like methods.
//
// A File is not safe for concurrent use: it carries a seek offset and caches the
// inode's block map on first use.
type File struct {
	volume *FS
	inode  Inode
	name   string
	offset uint64

	// extents caches the resolved block map so that a sequence of reads costs
	// one map resolution rather than one per call.
	extents    []Extent
	extentsErr error
	mapped     bool
}

// blockMap resolves and caches the file's block map.
func (f *File) blockMap() ([]Extent, error) {
	if !f.mapped {
		f.extents, f.extentsErr = f.volume.dataMap(f.inode)
		f.mapped = true
	}
	return f.extents, f.extentsErr
}

// Inode returns the inode backing the file.
func (f *File) Inode() Inode {
	return f.inode
}

// Name returns the display name if available.
func (f *File) Name() string {
	return f.name
}

// InodeNumber returns the backing inode number.
func (f *File) InodeNumber() uint32 {
	return f.inode.Number
}

// IsDirectory reports whether the inode is a directory.
func (f *File) IsDirectory() bool {
	return f.inode.IsDirectory
}

// Size returns the inode logical size.
func (f *File) Size() int64 {
	return int64(f.inode.Size)
}

// Read reads from the current file offset.
func (f *File) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, int64(f.offset))
	f.offset += uint64(n)
	return n, err
}

// ReadAt reads from a specific offset in a regular file.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.EOF
	}
	if f.inode.IsDirectory {
		return 0, ErrNotRegularFile
	}
	if len(p) == 0 {
		return 0, nil
	}

	if uint64(off) >= f.inode.Size {
		return 0, io.EOF
	}
	exts, err := f.blockMap()
	if err != nil {
		return 0, fmt.Errorf("inode %d block map: %w", f.inode.Number, err)
	}
	n, err := f.volume.readInodeDataAtMapped(f.inode, exts, uint64(off), p)
	if err != nil {
		return n, err
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// ReadAll reads the full contents of a regular file.
func (f *File) ReadAll() ([]byte, error) {
	if f.inode.IsDirectory {
		return nil, ErrNotRegularFile
	}
	return f.volume.readInodeData(f.inode)
}

// ReadLink reads the target of a symbolic link.
func (f *File) ReadLink() (string, error) {
	if !f.inode.IsSymlink {
		return "", ErrNotSymlink
	}

	// The inline test is the inode's block ownership, not its size: a 60-byte
	// target is stored in a block, and i_block then holds that block's map.
	if f.volume.isFastSymlink(f.inode) {
		raw := f.inode.BlockRaw[:f.inode.Size]
		return string(raw), nil
	}
	data, err := f.volume.readInodeData(f.inode)
	if err != nil {
		return "", err
	}
	if uint64(len(data)) > f.inode.Size {
		data = data[:f.inode.Size]
	}
	return string(data), nil
}

// ReadDir reads directory entries from a directory inode.
//
// Each entry's inode is read to resolve its type and size, so the timestamps,
// mode, ownership and deleted state come along at no additional cost.
func (f *File) ReadDir() ([]DirEntry, error) {
	if !f.inode.IsDirectory {
		return nil, ErrNotDirectory
	}
	entries, err := f.volume.ListDir(f.inode.Number)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		f.volume.fillEntryFromInode(&entries[i])
	}
	return entries, nil
}

// Timestamps returns the file's full MACB set.
func (f *File) Timestamps() Timestamps {
	return f.inode.Timestamps()
}

func (fs *FS) ReadFile(inodeNum uint32) ([]byte, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	if !inode.IsRegular {
		return nil, ErrNotRegularFile
	}
	return fs.readInodeData(inode)
}

// defaultReadAllLimit caps a whole-inode read when the image size is unknown.
const defaultReadAllLimit = 1 << 30

// readAllLimit bounds how much a single whole-inode read may allocate.
//
// The image size is the real bound: an inode cannot hold more bytes than the
// filesystem it lives in. A corrupt or crafted size field otherwise turns one
// read into a gigabyte allocation and a million block lookups.
func (fs *FS) readAllLimit() uint64 {
	limit := uint64(defaultReadAllLimit)
	if fs.imageSize > 0 && fs.imageSize < limit {
		limit = fs.imageSize
	}
	return limit
}

func (fs *FS) readInodeData(inode Inode) ([]byte, error) {
	data, _, err := fs.readInodeDataMapped(inode)
	return data, err
}

// readInodeDataMapped is readInodeData that also returns the block map the
// contents were read through, so a caller can name the physical block behind
// an offset without resolving the map again. The map is nil for an empty or
// inline inode, which own no blocks.
func (fs *FS) readInodeDataMapped(inode Inode) ([]byte, []Extent, error) {
	if inode.Size == 0 {
		return []byte{}, nil, nil
	}
	// An inline inode owns no blocks; its contents live in the inode itself.
	if inode.HasInline {
		raw, err := fs.readInodeRaw(inode.Number)
		if err != nil {
			return nil, nil, err
		}
		data, err := fs.inlineDataFromRaw(inode, raw)
		return data, nil, err
	}
	if limit := fs.readAllLimit(); inode.Size > limit {
		return nil, nil, fmt.Errorf("inode %d too large to read whole: %d bytes exceeds the %d byte limit",
			inode.Number, inode.Size, limit)
	}
	exts, err := fs.dataMap(inode)
	if err != nil {
		return nil, nil, fmt.Errorf("inode %d block map: %w", inode.Number, err)
	}
	buf := make([]byte, inode.Size)
	_, err = fs.readInodeDataAtMapped(inode, exts, 0, buf)
	if err != nil && err != io.EOF {
		return nil, nil, err
	}
	return buf, exts, nil
}

func (fs *FS) readInodeDataAt(inode Inode, off uint64, out []byte) (int, error) {
	exts, err := fs.dataMap(inode)
	if err != nil {
		return 0, fmt.Errorf("inode %d block map: %w", inode.Number, err)
	}
	return fs.readInodeDataAtMapped(inode, exts, off, out)
}

// dataMap is the block map a read of the inode's contents is served from. It
// is InodeExtents plus, under Options.Strict, the refusal of a map that leaves
// part of the file without written data.
func (fs *FS) dataMap(inode Inode) ([]Extent, error) {
	exts, err := fs.InodeExtents(inode, ExtentOptions{})
	if err != nil {
		return nil, err
	}
	if err := fs.strictCheckMap(inode, exts); err != nil {
		return nil, err
	}
	return exts, nil
}

// readInodeDataAtMapped serves a read from an already-resolved block map.
//
// Resolving the map once per read rather than once per block is what makes this
// linear: the previous path re-parsed the whole extent tree, or re-read an
// indirect block, for every block it copied.
func (fs *FS) readInodeDataAtMapped(inode Inode, exts []Extent, off uint64, out []byte) (int, error) {
	if off >= inode.Size {
		return 0, io.EOF
	}
	maxReadable := inode.Size - off
	if uint64(len(out)) < maxReadable {
		maxReadable = uint64(len(out))
	}

	blockSize := uint64(fs.sb.BlockSize)
	var copied uint64
	for copied < maxReadable {
		curOff := off + copied
		logical := curOff / blockSize
		inBlockOff := curOff % blockSize
		chunk := blockSize - inBlockOff
		if remain := maxReadable - copied; remain < chunk {
			chunk = remain
		}

		e, ok := lookupExtent(exts, logical)
		if ok && !e.Sparse() && !e.Unwritten() && !e.Inline() {
			physBlock := e.PhysicalBlock + (logical - e.LogicalBlock)
			blk, err := fs.readBlock(physBlock)
			if err != nil {
				return int(copied), err
			}
			copy(out[copied:copied+chunk], blk[inBlockOff:inBlockOff+chunk])
		} else {
			// A hole, a preallocated run, or an unmapped block reads as zeros.
			// out belongs to the caller and may hold anything, so the span must
			// be cleared rather than skipped.
			clear(out[copied : copied+chunk])
		}
		copied += chunk
	}

	if off+copied >= inode.Size {
		return int(copied), io.EOF
	}
	return int(copied), nil
}
