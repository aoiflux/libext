package libext

import "fmt"

// groupPrealloc bounds the descriptor slice reserved up front. Descriptors are
// appended as they are successfully read, so a superblock claiming an absurd
// group count cannot force a large allocation before the first failed read.
const groupPrealloc = 4096

func (fs *FS) loadGroupDescriptors() error {
	gdSize := int(fs.sb.GroupDescSize)
	if gdSize < 32 {
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
	if len(buf) < 32 {
		return GroupDescriptor{}, ErrUnsupportedLayout
	}
	gd := GroupDescriptor{
		Group:            group,
		BlockBitmapBlock: uint64(le32(buf, 0x00)),
		InodeBitmapBlock: uint64(le32(buf, 0x04)),
		InodeTableBlock:  uint64(le32(buf, 0x08)),
		FreeBlocksCount:  uint32(le16(buf, 0x0C)),
		FreeInodesCount:  uint32(le16(buf, 0x0E)),
		UsedDirsCount:    uint32(le16(buf, 0x10)),
		Flags:            le16(buf, 0x12),
	}

	if len(buf) >= 64 {
		gd.BlockBitmapBlock |= uint64(le32(buf, 0x20)) << 32
		gd.InodeBitmapBlock |= uint64(le32(buf, 0x24)) << 32
		gd.InodeTableBlock |= uint64(le32(buf, 0x28)) << 32
		gd.FreeBlocksCount |= uint32(le16(buf, 0x2C)) << 16
		gd.FreeInodesCount |= uint32(le16(buf, 0x2E)) << 16
		gd.UsedDirsCount |= uint32(le16(buf, 0x30)) << 16
	}

	return gd, nil
}
