package libext

import "fmt"

func (fs *FS) loadSuperblock() error {
	buf := make([]byte, superblockSize)
	if err := fs.readAt(superblockOffset, buf); err != nil {
		return fmt.Errorf("read superblock: %w", err)
	}

	sb := Superblock{}
	sb.InodesCount = le32(buf, sbOffInodesCount)
	blocksLo := le32(buf, sbOffBlocksCountLo)
	resBlocksLo := le32(buf, sbOffReservedBlocksLo)
	freeBlocksLo := le32(buf, sbOffFreeBlocksLo)
	sb.FreeInodes = le32(buf, sbOffFreeInodes)
	sb.FirstDataBlock = le32(buf, sbOffFirstDataBlock)
	sb.LogBlockSize = le32(buf, sbOffLogBlockSize)
	sb.BlockSize = 1024 << sb.LogBlockSize
	sb.BlocksPerGroup = le32(buf, sbOffBlocksPerGroup)
	sb.InodesPerGroup = le32(buf, sbOffInodesPerGroup)
	sb.MountTime = unixTime(le32(buf, sbOffMountTime))
	sb.WriteTime = unixTime(le32(buf, sbOffWriteTime))
	sb.MountCount = le16(buf, sbOffMountCount)
	sb.MaxMountCount = le16(buf, sbOffMaxMountCount)
	sb.Magic = le16(buf, sbOffMagic)
	sb.State = le16(buf, sbOffState)
	sb.ErrorsBehavior = le16(buf, sbOffErrorsBehavior)
	sb.RevisionLevel = le32(buf, sbOffRevisionLevel)
	sb.CreatorOS = le32(buf, sbOffCreatorOS)
	sb.FirstInode = le32(buf, sbOffFirstInode)
	sb.InodeSize = le16(buf, sbOffInodeSize)
	sb.FeatureCompat = le32(buf, sbOffFeatureCompat)
	sb.FeatureIncompat = le32(buf, sbOffFeatureIncompat)
	sb.FeatureROCompat = le32(buf, sbOffFeatureROCompat)
	copy(sb.UUID[:], buf[sbOffUUID:sbOffUUID+sbUUIDLen])
	sb.VolumeName = trimCString(buf[sbOffVolumeName : sbOffVolumeName+sbVolumeNameLen])
	sb.LastMounted = trimCString(buf[sbOffLastMounted : sbOffLastMounted+sbLastMountedLen])
	sb.JournalInode = le32(buf, sbOffJournalInode)
	sb.JournalDevice = le32(buf, sbOffJournalDevice)
	sb.LastOrphan = le32(buf, sbOffLastOrphan)
	sb.DescSize = le16(buf, sbOffDescSize)
	sb.ChecksumSeed = le32(buf, sbOffChecksumSeed)
	// s_orphan_file_inum sits at 0x280, after the four *_hi time bytes and the
	// encoding fields. Confirmed against a filesystem whose neighbouring
	// s_overhead_clusters and s_checksum_seed both match dumpe2fs.
	sb.OrphanFileInode = le32(buf, sbOffOrphanFileInode)

	if sb.Magic != extMagic {
		return ErrInvalidSuperblock
	}
	if sb.BlockSize < 1024 || sb.BlockSize > 65536 || sb.BlockSize%1024 != 0 {
		return fmt.Errorf("%w: invalid block size %d", ErrInvalidSuperblock, sb.BlockSize)
	}
	if sb.InodesPerGroup == 0 || sb.BlocksPerGroup == 0 {
		return fmt.Errorf("%w: zero group geometry", ErrInvalidSuperblock)
	}

	blocksHi := le32(buf, sbOffBlocksCountHi)
	resBlocksHi := le32(buf, sbOffReservedBlocksHi)
	freeBlocksHi := le32(buf, sbOffFreeBlocksHi)
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

	// The inode table is allocated per group, so the total inode count cannot
	// exceed what the groups actually hold. Without this, a crafted superblock
	// can claim billions of inodes on a tiny image and make any full-table scan
	// run effectively forever — the same exhaustion as an oversized group count,
	// spent in time rather than memory.
	if capacity := groups * uint64(sb.InodesPerGroup); uint64(sb.InodesCount) > capacity {
		return fmt.Errorf("%w: %d inodes exceeds the %d its %d groups can hold",
			ErrInvalidSuperblock, sb.InodesCount, capacity, groups)
	}

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
