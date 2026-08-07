package libext

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

var extCRC32CTable = crc32.MakeTable(crc32.Castagnoli)

const (
	// crc32cInit is the seed ext4 starts every checksum chain from (~0).
	crc32cInit = ^uint32(0)

	// superblockChecksumOffset is the offset of s_checksum, and therefore the
	// number of bytes the superblock checksum covers.
	superblockChecksumOffset = 0x3FC
)

// extCRC32C is the CRC-32C ext4 uses.
//
// It differs from hash/crc32 in one respect that invalidates every result if
// missed: Linux's crc32c is a running update with no final complement, whereas
// hash/crc32 complements both on entry and on exit. Undoing both complements
// turns the standard implementation into the one ext4 expects.
func extCRC32C(seed uint32, data []byte) uint32 {
	return ^crc32.Update(^seed, extCRC32CTable, data)
}

// csumSeed is the value that seeds group descriptor and inode checksums: the
// stored seed when the filesystem carries one, otherwise a checksum of the UUID.
func (fs *FS) csumSeed() uint32 {
	if (fs.sb.FeatureIncompat&featureIncompatCSumSeed) != 0 && fs.sb.ChecksumSeed != 0 {
		return fs.sb.ChecksumSeed
	}
	return extCRC32C(crc32cInit, fs.sb.UUID[:])
}

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

	// The checksum covers the bytes preceding s_checksum, not the whole
	// superblock with the field zeroed: the trailing zeros would be folded in.
	stored := le32(raw, superblockChecksumOffset)
	calc := extCRC32C(crc32cInit, raw[:superblockChecksumOffset])
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

	crc := extCRC32C(fs.csumSeed(), g)
	crc = extCRC32C(crc, desc)

	calc := uint16(crc & 0xFFFF)
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

	crc := extCRC32C(fs.csumSeed(), inum)
	crc = extCRC32C(crc, gen)
	calc := extCRC32C(crc, buf)

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

// bitmapChecksumWidth reports how many bits of a bitmap checksum are stored.
// The high half of the field exists only in a 64-bit group descriptor.
func (fs *FS) bitmapChecksumWidth() uint32 {
	if fs.sb.GroupDescSize >= 64 {
		return 32
	}
	return 16
}

// verifyBitmapChecksum compares a stored bitmap checksum against the data.
func (fs *FS) verifyBitmapChecksum(group uint32, stored uint32, data []byte, kind string) error {
	calc := extCRC32C(fs.csumSeed(), data)
	if fs.bitmapChecksumWidth() == 16 {
		calc &= 0xFFFF
		stored &= 0xFFFF
	}
	if stored != calc {
		return fmt.Errorf("%w: group=%d %s bitmap stored=0x%08x calc=0x%08x",
			ErrChecksumMismatch, group, kind, stored, calc)
	}
	return nil
}

// VerifyBlockBitmapChecksum validates a group's block bitmap against the
// checksum stored in its descriptor.
//
// blockBitmapData must be the bitmap as BlockBitmap returns it: BlocksPerGroup
// bits, which is usually shorter than the block holding it. Checksumming the
// whole block instead folds in trailing padding and never matches.
func (fs *FS) VerifyBlockBitmapChecksum(group uint32, blockBitmapData []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	gd, err := fs.group(group)
	if err != nil {
		return err
	}
	if gd.BlockUninit() {
		return nil
	}
	return fs.verifyBitmapChecksum(group, gd.BlockBitmapChecksum, blockBitmapData, "block")
}

// VerifyInodeBitmapChecksum validates a group's inode bitmap against the
// checksum stored in its descriptor. See VerifyBlockBitmapChecksum for the
// expected extent of inodeBitmapData.
func (fs *FS) VerifyInodeBitmapChecksum(group uint32, inodeBitmapData []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	gd, err := fs.group(group)
	if err != nil {
		return err
	}
	if gd.InodeUninit() {
		return nil
	}
	return fs.verifyBitmapChecksum(group, gd.InodeBitmapChecksum, inodeBitmapData, "inode")
}
