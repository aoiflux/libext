package libext

import "fmt"

func (fs *FS) loadSuperblock() error {
	buf := make([]byte, superblockSize)
	if err := fs.readAt(superblockOffset, buf); err != nil {
		return fmt.Errorf("read superblock: %w", err)
	}

	sb := Superblock{}
	sb.InodesCount = le32(buf, 0x00)
	blocksLo := le32(buf, 0x04)
	resBlocksLo := le32(buf, 0x08)
	freeBlocksLo := le32(buf, 0x0C)
	sb.FreeInodes = le32(buf, 0x10)
	sb.FirstDataBlock = le32(buf, 0x14)
	sb.LogBlockSize = le32(buf, 0x18)
	sb.BlockSize = 1024 << sb.LogBlockSize
	sb.BlocksPerGroup = le32(buf, 0x20)
	sb.InodesPerGroup = le32(buf, 0x28)
	sb.MountTime = unixTime(le32(buf, 0x2C))
	sb.WriteTime = unixTime(le32(buf, 0x30))
	sb.MountCount = le16(buf, 0x34)
	sb.MaxMountCount = le16(buf, 0x36)
	sb.Magic = le16(buf, 0x38)
	sb.State = le16(buf, 0x3A)
	sb.ErrorsBehavior = le16(buf, 0x3C)
	sb.RevisionLevel = le32(buf, 0x4C)
	sb.CreatorOS = le32(buf, 0x48)
	sb.FirstInode = le32(buf, 0x54)
	sb.InodeSize = le16(buf, 0x58)
	sb.FeatureCompat = le32(buf, 0x5C)
	sb.FeatureIncompat = le32(buf, 0x60)
	sb.FeatureROCompat = le32(buf, 0x64)
	copy(sb.UUID[:], buf[0x68:0x78])
	sb.VolumeName = trimCString(buf[0x78:0x88])
	sb.LastMounted = trimCString(buf[0x88:0xC8])
	sb.JournalInode = le32(buf, 0xE0)
	sb.JournalDevice = le32(buf, 0xE4)
	sb.LastOrphan = le32(buf, 0xE8)
	sb.DescSize = le16(buf, 0xFE)
	sb.ChecksumSeed = le32(buf, 0x270)

	if sb.Magic != extMagic {
		return ErrInvalidSuperblock
	}
	if sb.BlockSize < 1024 || sb.BlockSize > 65536 || sb.BlockSize%1024 != 0 {
		return fmt.Errorf("%w: invalid block size %d", ErrInvalidSuperblock, sb.BlockSize)
	}
	if sb.InodesPerGroup == 0 || sb.BlocksPerGroup == 0 {
		return fmt.Errorf("%w: zero group geometry", ErrInvalidSuperblock)
	}

	blocksHi := le32(buf, 0x150)
	resBlocksHi := le32(buf, 0x154)
	freeBlocksHi := le32(buf, 0x158)
	if (sb.FeatureIncompat&featureIncompat64Bit) != 0 || blocksHi != 0 {
		sb.BlocksCount = (uint64(blocksHi) << 32) | uint64(blocksLo)
		sb.ReservedBlocks = (uint64(resBlocksHi) << 32) | uint64(resBlocksLo)
		sb.FreeBlocks = (uint64(freeBlocksHi) << 32) | uint64(freeBlocksLo)
	} else {
		sb.BlocksCount = uint64(blocksLo)
		sb.ReservedBlocks = uint64(resBlocksLo)
		sb.FreeBlocks = uint64(freeBlocksLo)
	}
	if sb.InodeSize == 0 {
		if sb.RevisionLevel == 0 {
			sb.InodeSize = 128
		} else {
			sb.InodeSize = 256
		}
	}
	if sb.FirstInode == 0 {
		sb.FirstInode = 11
	}

	sb.GroupDescSize = 32
	if sb.DescSize >= 32 {
		sb.GroupDescSize = sb.DescSize
	}

	// Geometry must be validated before the group count is derived from it.
	// FirstDataBlock > BlocksCount underflows the subtraction below, yielding a
	// group count near 2^32 and an allocation of hundreds of gigabytes in
	// loadGroupDescriptors.
	if sb.BlocksCount == 0 {
		return fmt.Errorf("%w: zero block count", ErrInvalidSuperblock)
	}
	if uint64(sb.FirstDataBlock) >= sb.BlocksCount {
		return fmt.Errorf("%w: first data block %d beyond block count %d",
			ErrInvalidSuperblock, sb.FirstDataBlock, sb.BlocksCount)
	}

	dataBlocks := sb.BlocksCount - uint64(sb.FirstDataBlock)
	groups := (dataBlocks + uint64(sb.BlocksPerGroup) - 1) / uint64(sb.BlocksPerGroup)
	if groups == 0 {
		return fmt.Errorf("%w: no groups", ErrInvalidSuperblock)
	}
	if groups > maxBlockGroups {
		return fmt.Errorf("%w: %d block groups exceeds limit %d",
			ErrInvalidSuperblock, groups, maxBlockGroups)
	}
	sb.GroupsCount = uint32(groups)

	if sb.BlockSize == 1024 {
		sb.GroupDescTableOff = 2 * 1024
	} else {
		sb.GroupDescTableOff = uint64(sb.BlockSize)
	}

	fs.sb = sb

	// Checksum validation is diagnostic by default: many real-world images carry
	// stale checksums written by tools that do not maintain them. Set
	// Options.VerifyChecksums to make a mismatch fatal.
	if err := fs.verifySuperblockChecksum(buf); err != nil {
		if fs.opts.VerifyChecksums {
			return err
		}
		fs.warn(WarnChecksumMismatch, "", err.Error())
	}

	if fs.imageSize > 0 {
		if want := sb.BlocksCount * uint64(sb.BlockSize); want > fs.imageSize {
			fs.warn(WarnTruncatedImage, "", fmt.Sprintf(
				"superblock describes %d bytes but the reader holds %d; reads past the end will fail",
				want, fs.imageSize))
		}
	}

	fs.kind = detectFSKind(sb)
	return nil
}

func detectFSKind(sb Superblock) FSKind {
	if (sb.FeatureIncompat&featureIncompatExtents) != 0 ||
		(sb.FeatureIncompat&featureIncompat64Bit) != 0 ||
		(sb.FeatureROCompat&featureRoCompatMetadataCS) != 0 {
		return FSKindExt4
	}
	if (sb.FeatureCompat & featureCompatHasJournal) != 0 {
		return FSKindExt3
	}
	if sb.RevisionLevel == 0 {
		return FSKindExt1
	}
	return FSKindExt2
}
