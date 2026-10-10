package libext

import (
	"fmt"
)

func (fs *FS) ReadInode(inodeNum uint32) (Inode, error) {
	raw, err := fs.readInodeRaw(inodeNum)
	if err != nil {
		return Inode{}, err
	}
	return parseInode(raw, inodeNum), nil
}

// readInodeRaw returns an inode's on-disk bytes. The trailing area beyond the
// fixed fields holds extended attributes and inline data, so callers that need
// those work from the raw form rather than the parsed one.
func (fs *FS) readInodeRaw(inodeNum uint32) ([]byte, error) {
	block, offInBlock, err := fs.inodeTableLocation(inodeNum)
	if err != nil {
		return nil, err
	}
	off := fs.blockOffset(block) + offInBlock

	raw := make([]byte, fs.sb.InodeSize)
	if err := fs.readAt(off, raw); err != nil {
		return nil, fmt.Errorf("read inode %d: %w", inodeNum, err)
	}
	// Checksum validation is diagnostic unless Options.VerifyChecksums is set:
	// stale checksums are common in images written by tools that do not
	// maintain them, and refusing to read those would be worse than reporting.
	if err := fs.verifyInodeChecksum(inodeNum, raw); err != nil {
		if fs.opts.VerifyChecksums {
			return nil, err
		}
	}
	return raw, nil
}

func parseInode(raw []byte, inodeNum uint32) Inode {
	mode := le16(raw, InodeOffMode)
	flags := le32(raw, InodeOffFlags)
	sizeLo := le32(raw, InodeOffSizeLo)
	sizeHi := le32(raw, InodeOffSizeHi)

	uid := uint32(le16(raw, InodeOffUIDLo))
	gid := uint32(le16(raw, InodeOffGIDLo))
	blocks := uint64(le32(raw, InodeOffBlocksLo))
	fileACL := uint64(le32(raw, InodeOffFileACLLo))

	if len(raw) >= GoodOldInodeSize {
		uid |= uint32(le16(raw, InodeOffUIDHi)) << 16
		gid |= uint32(le16(raw, InodeOffGIDHi)) << 16
		// The high halves live in osd2 and were previously dropped, which put
		// large-volume xattr blocks out of reach and truncated block counts.
		blocks |= uint64(le16(raw, InodeOffBlocksHi)) << 32
		fileACL |= uint64(le16(raw, InodeOffFileACLHi)) << 32
	}

	isDir := (mode & ModeTypeMask) == ModeDir
	isRegular := (mode & ModeTypeMask) == ModeRegular

	// i_size_high is i_dir_acl for directories on ext2/ext3. Only widen the size
	// where the field really is the high word.
	size := uint64(sizeLo)
	if !isDir {
		size |= uint64(sizeHi) << 32
	}

	inode := Inode{
		Number:      inodeNum,
		Mode:        mode,
		UID:         uid,
		GID:         gid,
		Size:        size,
		LinksCount:  le16(raw, InodeOffLinksCount),
		Blocks512:   blocks,
		Flags:       flags,
		Generation:  le32(raw, InodeOffGeneration),
		FileACL:     fileACL,
		HasExtents:  (flags & InodeFlagExtents) != 0,
		HasInline:   (flags & InodeFlagInlineData) != 0,
		HugeFile:    (flags & InodeFlagHugeFile) != 0,
		IsDirectory: isDir,
		IsRegular:   isRegular,
		IsSymlink:   (mode & ModeTypeMask) == ModeSymlink,
	}
	copy(inode.BlockRaw[:], raw[InodeOffBlockRaw:InodeOffBlockRaw+60])

	// The v1 timestamps are 32-bit seconds. ext4 widens them with a companion
	// "extra" word whose low two bits extend the epoch and whose upper 30 bits
	// carry nanoseconds. Without it every timestamp past 2038-01-19 decodes as a
	// pre-1970 date.
	extraEnd := 0
	if len(raw) > InodeOffExtraISize+2 {
		inode.ExtraISize = le16(raw, InodeOffExtraISize)
		extraEnd = GoodOldInodeSize + int(inode.ExtraISize)
		if extraEnd > len(raw) {
			extraEnd = len(raw)
		}
	}

	hasExtra := func(off int) bool { return off+4 <= extraEnd }

	inode.Atime = decodeTime(le32(raw, InodeOffAtime), extraAt(raw, InodeOffAtimeExtra, hasExtra))
	inode.Ctime = decodeTime(le32(raw, InodeOffCtime), extraAt(raw, InodeOffCtimeExtra, hasExtra))
	inode.Mtime = decodeTime(le32(raw, InodeOffMtime), extraAt(raw, InodeOffMtimeExtra, hasExtra))

	// Deletion time has no extra word: it is always 32-bit seconds.
	inode.DtimeRaw = le32(raw, InodeOffDtime)
	inode.Dtime = unixTime(inode.DtimeRaw)

	if hasExtra(InodeOffCrtime) {
		inode.Crtime = decodeTime(le32(raw, InodeOffCrtime), extraAt(raw, InodeOffCrtimeExtra, hasExtra))
		inode.HasCrtime = true
	}
	if hasExtra(InodeOffProjectID) {
		inode.ProjectID = le32(raw, InodeOffProjectID)
	}

	return inode
}

// extraAt returns the extra timestamp word at off, or 0 when the inode is too
// small to carry it.
func extraAt(raw []byte, off int, hasExtra func(int) bool) uint32 {
	if !hasExtra(off) {
		return 0
	}
	return le32(raw, off)
}
