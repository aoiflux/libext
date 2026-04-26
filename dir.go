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
	return fs.parseDirEntries(data)
}

func (fs *FS) parseDirEntries(data []byte) ([]DirEntry, error) {
	entries := make([]DirEntry, 0, 32)
	for off := 0; off+8 <= len(data); {
		inode := le32(data, off)
		recLen := le16(data, off+4)
		if recLen == 0 {
			break
		}
		if int(recLen) < 8 || off+int(recLen) > len(data) {
			return nil, ErrUnsupportedLayout
		}

		nameLen := uint8(data[off+6])
		fileType := uint8(data[off+7])
		if (fs.sb.FeatureIncompat & featureIncompatFileType) == 0 {
			nameLen16 := le16(data, off+6)
			if nameLen16 > 255 {
				return nil, ErrUnsupportedLayout
			}
			nameLen = uint8(nameLen16)
			fileType = 0
		}
		if int(nameLen) > int(recLen)-8 {
			return nil, ErrUnsupportedLayout
		}
		name := string(data[off+8 : off+8+int(nameLen)])

		if inode != 0 {
			isDir := false
			if (fs.sb.FeatureIncompat&featureIncompatFileType) != 0 && fileType == extDirentTypeDirectory {
				isDir = true
			}
			entries = append(entries, DirEntry{
				Inode:       inode,
				RecLen:      recLen,
				NameLen:     nameLen,
				FileType:    fileType,
				Name:        name,
				IsDirectory: isDir,
			})
		}
		off += int(recLen)
	}
	return entries, nil
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
