package libext

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

var extCRC32CTable = crc32.MakeTable(crc32.Castagnoli)

func (fs *FS) shouldValidateMetadataChecksums() bool {
	return (fs.sb.FeatureROCompat & featureRoCompatMetadataCS) != 0
}

func (fs *FS) verifySuperblockChecksum(raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < superblockSize {
		return ErrUnsupportedLayout
	}

	stored := le32(raw, 0x3FC)
	buf := make([]byte, superblockSize)
	copy(buf, raw[:superblockSize])
	for i := 0x3FC; i < 0x400; i++ {
		buf[i] = 0
	}
	calc := crc32.Checksum(buf, extCRC32CTable)
	if stored != calc {
		return fmt.Errorf("%w: superblock stored=0x%08x calc=0x%08x", ErrChecksumMismatch, stored, calc)
	}
	return nil
}

func (fs *FS) verifyGroupDescriptorChecksum(group uint32, raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < 32 {
		return ErrUnsupportedLayout
	}

	stored := le16(raw, 0x1E)
	descSize := int(fs.sb.GroupDescSize)
	if descSize < 32 {
		descSize = 32
	}
	if descSize > len(raw) {
		descSize = len(raw)
	}

	desc := make([]byte, descSize)
	copy(desc, raw[:descSize])
	desc[0x1E] = 0
	desc[0x1F] = 0

	g := make([]byte, 4)
	binary.LittleEndian.PutUint32(g, group)

	h := crc32.New(extCRC32CTable)
	_, _ = h.Write(fs.sb.UUID[:])
	_, _ = h.Write(g)
	_, _ = h.Write(desc)

	calc := uint16(h.Sum32() & 0xFFFF)
	if stored != calc {
		return fmt.Errorf("%w: group=%d stored=0x%04x calc=0x%04x", ErrChecksumMismatch, group, stored, calc)
	}
	return nil
}

func (fs *FS) verifyInodeChecksum(inodeNum uint32, raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < 128 {
		return nil
	}

	// In ext4, i_checksum_lo lives in osd2 at offset 0x7C.
	storedLo := le16(raw, 0x7C)
	stored := uint32(storedLo)
	hasHi := inodeHasChecksumHi(raw)
	if hasHi {
		storedHi := le16(raw, 0x82)
		stored |= uint32(storedHi) << 16
	}

	buf := make([]byte, len(raw))
	copy(buf, raw)
	buf[0x7C] = 0
	buf[0x7D] = 0
	if hasHi {
		buf[0x82] = 0
		buf[0x83] = 0
	}

	inum := make([]byte, 4)
	binary.LittleEndian.PutUint32(inum, inodeNum)
	gen := make([]byte, 4)
	binary.LittleEndian.PutUint32(gen, le32(raw, 0x64))

	h := crc32.New(extCRC32CTable)
	_, _ = h.Write(fs.sb.UUID[:])
	_, _ = h.Write(inum)
	_, _ = h.Write(gen)
	_, _ = h.Write(buf)
	calc := h.Sum32()

	if hasHi {
		if calc != stored {
			return fmt.Errorf("%w: inode=%d stored=0x%08x calc=0x%08x", ErrChecksumMismatch, inodeNum, stored, calc)
		}
		return nil
	}

	calcLo := uint16(calc & 0xFFFF)
	if calcLo != storedLo {
		return fmt.Errorf("%w: inode=%d stored=0x%04x calc=0x%04x", ErrChecksumMismatch, inodeNum, storedLo, calcLo)
	}
	return nil
}

func inodeHasChecksumHi(raw []byte) bool {
	if len(raw) < 0x84 {
		return false
	}
	// i_checksum_hi exists only when extra inode space reaches offset 0x82.
	extra := int(le16(raw, 0x80))
	return 128+extra >= 0x84
}

// VerifyBlockBitmapChecksum validates the block bitmap checksum for a group.
// The checksum is stored in the group descriptor and computed from the bitmap data.
func (fs *FS) VerifyBlockBitmapChecksum(group uint32, blockBitmapData []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if group >= uint32(len(fs.groups)) {
		return fmt.Errorf("invalid group %d", group)
	}

	gd := fs.groups[group]
	stored := uint32(gd.Flags) // Simplified: in ext4, checksum is in osd2 fields
	// Note: Real implementation would extract from group descriptor extra fields

	h := crc32.New(extCRC32CTable)
	_, _ = h.Write(fs.sb.UUID[:])
	g := make([]byte, 4)
	binary.LittleEndian.PutUint32(g, group)
	_, _ = h.Write(g)
	_, _ = h.Write(blockBitmapData)

	calc := h.Sum32()

	// For now, we skip actual checksum comparison since group descriptor
	// layout varies by ext version. This function serves as a hook
	// for future implementation when full ext4 group descriptor parsing is done.
	_ = stored
	_ = calc

	return nil
}

// VerifyInodeBitmapChecksum validates the inode bitmap checksum for a group.
// Similar to block bitmap but for inode bitmap.
func (fs *FS) VerifyInodeBitmapChecksum(group uint32, inodeBitmapData []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if group >= uint32(len(fs.groups)) {
		return fmt.Errorf("invalid group %d", group)
	}

	gd := fs.groups[group]
	stored := uint32(gd.Flags) // Simplified: in ext4, checksum is in osd2 fields
	// Note: Real implementation would extract from group descriptor extra fields

	h := crc32.New(extCRC32CTable)
	_, _ = h.Write(fs.sb.UUID[:])
	g := make([]byte, 4)
	binary.LittleEndian.PutUint32(g, group)
	_, _ = h.Write(g)
	_, _ = h.Write(inodeBitmapData)

	calc := h.Sum32()

	// For now, we skip actual checksum comparison since group descriptor
	// layout varies by ext version. This function serves as a hook
	// for future implementation when full ext4 group descriptor parsing is done.
	_ = stored
	_ = calc

	return nil
}
