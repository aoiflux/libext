package libext

import (
	"fmt"
	"path"
	"strings"
)

// Volume is the primary EXT parser type.
// It is an alias of FS for API compatibility.
type Volume = FS

// Open opens a file or directory by inode number.
func (fs *FS) Open(inodeNum uint32) (*File, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	return &File{
		volume: fs,
		inode:  inode,
		name:   fmt.Sprintf("inode:%d", inodeNum),
	}, nil
}

// GetRootDirectory returns the root directory handle.
func (fs *FS) GetRootDirectory() (*File, error) {
	return fs.Open(RootInode)
}

// OpenPath opens a file or directory by absolute or relative path.
func (fs *FS) OpenPath(filePath string) (*File, error) {
	clean := normalizeEXTPath(filePath)
	if clean == "/" {
		return fs.GetRootDirectory()
	}

	current, err := fs.GetRootDirectory()
	if err != nil {
		return nil, err
	}

	parts := strings.Split(strings.Trim(clean, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if !current.IsDirectory() {
			return nil, ErrNotDirectory
		}

		entries, err := current.ReadDir()
		if err != nil {
			return nil, err
		}

		matched := false
		for _, e := range entries {
			if e.Name == part {
				current, err = fs.Open(e.Inode)
				if err != nil {
					return nil, err
				}
				current.name = e.Name
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("%w: %s", ErrPathNotFound, clean)
		}
	}

	return current, nil
}

func normalizeEXTPath(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	filePath = path.Clean(filePath)
	if filePath == "" || filePath == "." {
		return "/"
	}
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	return filePath
}
