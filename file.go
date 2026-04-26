package libext

import (
	"fmt"
	"io"
)

// File represents an opened EXT inode with file-like methods.
type File struct {
	volume *FS
	inode  Inode
	name   string
	offset uint64
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
	n, err := f.volume.readInodeDataAt(f.inode, uint64(off), p)
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

		physBlock, mapped, err := fs.inodeBlockNumber(inode, logical)
		if err != nil {
			return int(copied), fmt.Errorf("inode %d block map: %w", inode.Number, err)
		}
		if mapped {
			blk, err := fs.readBlock(physBlock)
			if err != nil {
				return int(copied), err
			}
			start := int(copied)
			end := int(copied + chunk)
			blkStart := int(inBlockOff)
			blkEnd := int(inBlockOff + chunk)
			copy(out[start:end], blk[blkStart:blkEnd])
		}
		copied += chunk
	}

	if off+copied >= inode.Size {
		return int(copied), io.EOF
	}
	return int(copied), nil
}
