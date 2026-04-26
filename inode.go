package libext

import (
	"fmt"
)

func (fs *FS) ReadInode(inodeNum uint32) (Inode, error) {
	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return Inode{}, ErrInvalidInode
	}
	group := (inodeNum - 1) / fs.sb.InodesPerGroup
	index := (inodeNum - 1) % fs.sb.InodesPerGroup
	if group >= uint32(len(fs.groups)) {
		return Inode{}, ErrInvalidInode
	}
	gd := fs.groups[group]
	inodeTableOff := fs.blockOffset(gd.InodeTableBlock)
	off := inodeTableOff + uint64(index)*uint64(fs.sb.InodeSize)

	raw := make([]byte, fs.sb.InodeSize)
	if err := fs.readAt(off, raw); err != nil {
		return Inode{}, fmt.Errorf("read inode %d: %w", inodeNum, err)
	}
	// Inode checksum validation is optional and non-fatal
	_ = fs.verifyInodeChecksum(inodeNum, raw) // diagnostic only, don't fail on mismatch
	return parseInode(raw, inodeNum), nil
}

func parseInode(raw []byte, inodeNum uint32) Inode {
	mode := le16(raw, 0x00)
	uidLo := le16(raw, 0x02)
	sizeLo := le32(raw, 0x04)
	atime := le32(raw, 0x08)
	ctime := le32(raw, 0x0C)
	mtime := le32(raw, 0x10)
	dtime := le32(raw, 0x14)
	gidLo := le16(raw, 0x18)
	links := le16(raw, 0x1A)
	blocksLo := le32(raw, 0x1C)
	flags := le32(raw, 0x20)
	generation := le32(raw, 0x64)
	fileACLLo := le32(raw, 0x68)
	sizeHi := le32(raw, 0x6C)

	uid := uint32(uidLo)
	gid := uint32(gidLo)
	if len(raw) >= 128 {
		uidHi := le16(raw, 0x78)
		gidHi := le16(raw, 0x7A)
		uid |= uint32(uidHi) << 16
		gid |= uint32(gidHi) << 16
	}

	inode := Inode{
		Number:      inodeNum,
		Mode:        mode,
		UID:         uid,
		GID:         gid,
		Size:        (uint64(sizeHi) << 32) | uint64(sizeLo),
		Atime:       unixTime(atime),
		Ctime:       unixTime(ctime),
		Mtime:       unixTime(mtime),
		Dtime:       unixTime(dtime),
		LinksCount:  links,
		Blocks512:   uint64(blocksLo),
		Flags:       flags,
		Generation:  generation,
		FileACL:     uint64(fileACLLo),
		HasExtents:  (flags & inodeFlagExtents) != 0,
		IsDirectory: (mode & inodeModeTypeMask) == inodeTypeDir,
		IsRegular:   (mode & inodeModeTypeMask) == inodeTypeRegular,
		IsSymlink:   (mode & inodeModeTypeMask) == inodeTypeSymlink,
	}
	copy(inode.BlockRaw[:], raw[0x28:0x28+60])

	return inode
}
