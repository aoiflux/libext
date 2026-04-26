package libext

import "fmt"

func (fs *FS) loadGroupDescriptors() error {
	gdSize := int(fs.sb.GroupDescSize)
	buf := make([]byte, gdSize)
	fs.groups = make([]GroupDescriptor, fs.sb.GroupsCount)

	for i := uint32(0); i < fs.sb.GroupsCount; i++ {
		off := fs.sb.GroupDescTableOff + uint64(i)*uint64(gdSize)
		if err := fs.readAt(off, buf); err != nil {
			return fmt.Errorf("read group descriptor %d: %w", i, err)
		}
		// Group descriptor checksum validation is optional and non-fatal
		_ = fs.verifyGroupDescriptorChecksum(i, buf) // diagnostic only, don't fail on mismatch
		gd, err := parseGroupDescriptor(buf, i)
		if err != nil {
			return fmt.Errorf("parse group descriptor %d: %w", i, err)
		}
		fs.groups[i] = gd
	}

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
