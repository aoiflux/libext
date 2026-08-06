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
		f.extents, f.extentsErr = f.volume.InodeExtents(f.inode, ExtentOptions{})
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

	if f.inode.Size <= 60 {
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
func (f *File) ReadDir() ([]DirEntry, error) {
	if !f.inode.IsDirectory {
		return nil, ErrNotDirectory
	}
	entries, err := f.volume.ListDir(f.inode.Number)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		child, err := f.volume.ReadInode(entries[i].Inode)
		if err == nil {
			entries[i].IsDirectory = child.IsDirectory
			entries[i].Size = child.Size
		}
	}
	return entries, nil
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

func (fs *FS) readInodeData(inode Inode) ([]byte, error) {
	if inode.Size == 0 {
		return []byte{}, nil
	}
	if inode.Size > (1 << 30) {
		return nil, fmt.Errorf("inode %d too large: %d bytes", inode.Number, inode.Size)
	}
	buf := make([]byte, inode.Size)
	_, err := fs.readInodeDataAt(inode, 0, buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (fs *FS) readInodeDataAt(inode Inode, off uint64, out []byte) (int, error) {
	exts, err := fs.InodeExtents(inode, ExtentOptions{})
	if err != nil {
		return 0, fmt.Errorf("inode %d block map: %w", inode.Number, err)
	}
	return fs.readInodeDataAtMapped(inode, exts, off, out)
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
