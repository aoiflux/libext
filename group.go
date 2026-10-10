package libext

import "fmt"

// groupPrealloc bounds the descriptor slice reserved up front. Descriptors are
// appended as they are successfully read, so a superblock claiming an absurd
// group count cannot force a large allocation before the first failed read.
const groupPrealloc = 4096

func (fs *FS) loadGroupDescriptors() error {
	gdSize := int(fs.sb.GroupDescSize)
	if gdSize < GroupDescSizeMin {
		return fmt.Errorf("%w: group descriptor size %d", ErrInvalidSuperblock, gdSize)
	}

	count := uint64(fs.sb.GroupsCount)
	if fs.imageSize > 0 {
		tableBytes := count * uint64(gdSize)
		off := fs.sb.GroupDescTableOff
		if off >= fs.imageSize || tableBytes > fs.imageSize-off {
			detail := fmt.Sprintf(
				"group descriptor table (%d groups, %d bytes at offset %d) exceeds image size %d",
				count, tableBytes, off, fs.imageSize)
			if !fs.opts.Permissive {
				return fmt.Errorf("%w: %s", ErrInvalidSuperblock, detail)
			}
			fs.warn(WarnTruncatedImage, "", detail)
		}
	}

	prealloc := count
	if prealloc > groupPrealloc {
		prealloc = groupPrealloc
	}

	buf := make([]byte, gdSize)
	groups := make([]GroupDescriptor, 0, prealloc)

	for i := uint32(0); i < fs.sb.GroupsCount; i++ {
		off := fs.sb.GroupDescTableOff + uint64(i)*uint64(gdSize)
		if err := fs.readAt(off, buf); err != nil {
			if fs.opts.Permissive && len(groups) > 0 {
				fs.warn(WarnTruncatedImage, "", fmt.Sprintf(
					"group descriptor %d of %d is unreadable: %v", i, fs.sb.GroupsCount, err))
				break
			}
			return fmt.Errorf("read group descriptor %d: %w", i, err)
		}
		if err := fs.verifyGroupDescriptorChecksum(i, buf); err != nil {
			if fs.opts.VerifyChecksums {
				return err
			}
			fs.warn(WarnChecksumMismatch, "", err.Error())
		}
		gd, err := parseGroupDescriptor(buf, i)
		if err != nil {
			return fmt.Errorf("parse group descriptor %d: %w", i, err)
		}
		groups = append(groups, gd)
	}

	fs.groups = groups
	return nil
}

func parseGroupDescriptor(buf []byte, group uint32) (GroupDescriptor, error) {
	if len(buf) < GroupDescSizeMin {
		return GroupDescriptor{}, ErrUnsupportedLayout
	}
	gd := GroupDescriptor{
		Group:               group,
		BlockBitmapBlock:    uint64(le32(buf, GdOffBlockBitmapLo)),
		InodeBitmapBlock:    uint64(le32(buf, GdOffInodeBitmapLo)),
		InodeTableBlock:     uint64(le32(buf, GdOffInodeTableLo)),
		FreeBlocksCount:     uint32(le16(buf, GdOffFreeBlocksLo)),
		FreeInodesCount:     uint32(le16(buf, GdOffFreeInodesLo)),
		UsedDirsCount:       uint32(le16(buf, GdOffUsedDirsLo)),
		Flags:               le16(buf, GdOffFlags),
		BlockBitmapChecksum: uint32(le16(buf, GdOffBlockBitmapCSLo)),
		InodeBitmapChecksum: uint32(le16(buf, GdOffInodeBitmapCSLo)),
		ItableUnused:        uint32(le16(buf, GdOffItableUnusedLo)),
		Checksum:            le16(buf, GdOffChecksum),
	}

	if len(buf) >= GroupDescSize64Bit {
		gd.BlockBitmapBlock |= uint64(le32(buf, GdOffBlockBitmapHi)) << 32
		gd.InodeBitmapBlock |= uint64(le32(buf, GdOffInodeBitmapHi)) << 32
		gd.InodeTableBlock |= uint64(le32(buf, GdOffInodeTableHi)) << 32
		gd.FreeBlocksCount |= uint32(le16(buf, GdOffFreeBlocksHi)) << 16
		gd.FreeInodesCount |= uint32(le16(buf, GdOffFreeInodesHi)) << 16
		gd.UsedDirsCount |= uint32(le16(buf, GdOffUsedDirsHi)) << 16
		gd.ItableUnused |= uint32(le16(buf, GdOffItableUnusedHi)) << 16
		gd.BlockBitmapChecksum |= uint32(le16(buf, GdOffBlockBitmapCSHi)) << 16
		gd.InodeBitmapChecksum |= uint32(le16(buf, GdOffInodeBitmapCSHi)) << 16
	}

	return gd, nil
}

// HasSuperBackup reports whether a block group holds a copy of the superblock
// and group descriptor table, as the kernel's ext4_bg_has_super decides it.
//
// Group 0 always does. With sparse_super2 (CompatSparseSuper2) only the two
// groups named in s_backup_bgs do, and a zero there names no group. Otherwise,
// without sparse_super (ROCompatSparseSuper) every group does, and with it
// group 1 and the powers of 3, 5 and 7.
//
// sparseSuper2 is a separate argument because backupBGs alone cannot say
// whether the feature is on: sparse_super2 with both entries zero is a valid
// filesystem with no backups at all.
func HasSuperBackup(group uint32, sparseSuper, sparseSuper2 bool, backupBGs [2]uint32) bool {
	if group == 0 {
		return true
	}
	if sparseSuper2 {
		return group == backupBGs[0] || group == backupBGs[1]
	}
	if group <= 1 || !sparseSuper {
		return true
	}
	if group&1 == 0 {
		return false
	}
	return isPowerOf(group, 3) || isPowerOf(group, 5) || isPowerOf(group, 7)
}

// isPowerOf reports whether n is a positive power of base (test_root).
func isPowerOf(n, base uint32) bool {
	p := uint64(base)
	for p < uint64(n) {
		p *= uint64(base)
	}
	return p == uint64(n)
}
