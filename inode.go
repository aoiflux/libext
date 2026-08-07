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
	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return nil, ErrInvalidInode
	}
	group := (inodeNum - 1) / fs.sb.InodesPerGroup
	index := (inodeNum - 1) % fs.sb.InodesPerGroup
	if group >= uint32(len(fs.groups)) {
		return nil, ErrInvalidInode
	}
	gd := fs.groups[group]
	off := fs.blockOffset(gd.InodeTableBlock) + uint64(index)*uint64(fs.sb.InodeSize)

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

// Inode field offsets. Named because several of them are easy to transpose and
// the consequences are silent rather than loud.
const (
	inodeOffMode        = 0x00
	inodeOffUIDLo       = 0x02
	inodeOffSizeLo      = 0x04
	inodeOffAtime       = 0x08
	inodeOffCtime       = 0x0C
	inodeOffMtime       = 0x10
	inodeOffDtime       = 0x14
	inodeOffGIDLo       = 0x18
	inodeOffLinksCount  = 0x1A
	inodeOffBlocksLo    = 0x1C
	inodeOffFlags       = 0x20
	inodeOffBlockRaw    = 0x28
	inodeOffGeneration  = 0x64
	inodeOffFileACLLo   = 0x68
	inodeOffSizeHi      = 0x6C
	inodeOffBlocksHi    = 0x74 // osd2.linux2.l_i_blocks_hi
	inodeOffFileACLHi   = 0x76 // osd2.linux2.l_i_file_acl_high
	inodeOffUIDHi       = 0x78
	inodeOffGIDHi       = 0x7A
	inodeOffExtraISize  = 0x80
	inodeOffCtimeExtra  = 0x84
	inodeOffMtimeExtra  = 0x88
	inodeOffAtimeExtra  = 0x8C
	inodeOffCrtime      = 0x90
	inodeOffCrtimeExtra = 0x94
	inodeOffProjectID   = 0x9C

	// inodeBaseSize is the size of the fixed part; anything beyond it exists
	// only when i_extra_isize says so.
	inodeBaseSize = 128
)

// inodeFlagHugeFile makes i_blocks count filesystem blocks instead of 512-byte
// sectors.
const inodeFlagHugeFile = 0x00040000

func parseInode(raw []byte, inodeNum uint32) Inode {
	mode := le16(raw, inodeOffMode)
	flags := le32(raw, inodeOffFlags)
	sizeLo := le32(raw, inodeOffSizeLo)
	sizeHi := le32(raw, inodeOffSizeHi)

	uid := uint32(le16(raw, inodeOffUIDLo))
	gid := uint32(le16(raw, inodeOffGIDLo))
	blocks := uint64(le32(raw, inodeOffBlocksLo))
	fileACL := uint64(le32(raw, inodeOffFileACLLo))

	if len(raw) >= inodeBaseSize {
		uid |= uint32(le16(raw, inodeOffUIDHi)) << 16
		gid |= uint32(le16(raw, inodeOffGIDHi)) << 16
		// The high halves live in osd2 and were previously dropped, which put
		// large-volume xattr blocks out of reach and truncated block counts.
		blocks |= uint64(le16(raw, inodeOffBlocksHi)) << 32
		fileACL |= uint64(le16(raw, inodeOffFileACLHi)) << 32
	}

	isDir := (mode & inodeModeTypeMask) == inodeTypeDir
	isRegular := (mode & inodeModeTypeMask) == inodeTypeRegular

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
		LinksCount:  le16(raw, inodeOffLinksCount),
		Blocks512:   blocks,
		Flags:       flags,
		Generation:  le32(raw, inodeOffGeneration),
		FileACL:     fileACL,
		HasExtents:  (flags & inodeFlagExtents) != 0,
		HasInline:   (flags & inodeFlagInlineData) != 0,
		HugeFile:    (flags & inodeFlagHugeFile) != 0,
		IsDirectory: isDir,
		IsRegular:   isRegular,
		IsSymlink:   (mode & inodeModeTypeMask) == inodeTypeSymlink,
	}
	copy(inode.BlockRaw[:], raw[inodeOffBlockRaw:inodeOffBlockRaw+60])

	// The v1 timestamps are 32-bit seconds. ext4 widens them with a companion
	// "extra" word whose low two bits extend the epoch and whose upper 30 bits
	// carry nanoseconds. Without it every timestamp past 2038-01-19 decodes as a
	// pre-1970 date.
	extraEnd := 0
	if len(raw) > inodeOffExtraISize+2 {
		inode.ExtraISize = le16(raw, inodeOffExtraISize)
		extraEnd = inodeBaseSize + int(inode.ExtraISize)
		if extraEnd > len(raw) {
			extraEnd = len(raw)
		}
	}

	hasExtra := func(off int) bool { return off+4 <= extraEnd }

	inode.Atime = decodeTime(le32(raw, inodeOffAtime), extraAt(raw, inodeOffAtimeExtra, hasExtra))
	inode.Ctime = decodeTime(le32(raw, inodeOffCtime), extraAt(raw, inodeOffCtimeExtra, hasExtra))
	inode.Mtime = decodeTime(le32(raw, inodeOffMtime), extraAt(raw, inodeOffMtimeExtra, hasExtra))

	// Deletion time has no extra word: it is always 32-bit seconds.
	inode.DtimeRaw = le32(raw, inodeOffDtime)
	inode.Dtime = unixTime(inode.DtimeRaw)

	if hasExtra(inodeOffCrtime) {
		inode.Crtime = decodeTime(le32(raw, inodeOffCrtime), extraAt(raw, inodeOffCrtimeExtra, hasExtra))
		inode.HasCrtime = true
	}
	if hasExtra(inodeOffProjectID) {
		inode.ProjectID = le32(raw, inodeOffProjectID)
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
