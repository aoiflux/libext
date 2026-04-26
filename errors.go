package libext

import "errors"

var (
	ErrInvalidSuperblock = errors.New("invalid ext superblock")
	ErrChecksumMismatch  = errors.New("ext metadata checksum mismatch")
	ErrUnsupportedLayout = errors.New("unsupported ext filesystem layout")
	ErrInvalidInode      = errors.New("invalid inode")
	ErrNotDirectory      = errors.New("inode is not a directory")
	ErrNotRegularFile    = errors.New("inode is not a regular file")
	ErrNotSymlink        = errors.New("inode is not a symlink")
	ErrPathNotFound      = errors.New("path not found")
)
