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
)

// Metadata checksum codecs.
//
// Each function below computes one metadata_csum value exactly as the kernel
// does, from bytes the caller supplies. None of them reads the image, so a
// writer can stamp a structure with the same code this package verifies it
// with. Every function whose result is stored in a 16-bit field says so; the
// caller stores the low half.

// CRC32C is the CRC-32C ext4 uses: ext4_chksum, the kernel's crc32c.
//
// It differs from hash/crc32 in one respect that invalidates every result if
// missed: Linux's crc32c is a running update with no final complement, whereas
// hash/crc32 complements both on entry and on exit. Undoing both complements
// turns the standard implementation into the one ext4 expects. Chaining is how
// ext4 builds every checksum - CRC32C(CRC32C(seed, a), b) equals
// CRC32C(seed, a+b) - and every checksum chain starts from a seed of ^0.
func CRC32C(seed uint32, b []byte) uint32 {
	return ^crc32.Update(^seed, extCRC32CTable, b)
}

// MetadataSeed is the filesystem-wide seed of every group descriptor, inode,
// bitmap, extent block and directory block checksum (sbi->s_csum_seed).
//
// With INCOMPAT_CSUM_SEED (hasCsumSeed) it is s_checksum_seed as stored, which
// lets the UUID change without rewriting every checksum; otherwise it is the
// CRC32C of the UUID from a seed of ^0.
func MetadataSeed(uuid [16]byte, csumSeed uint32, hasCsumSeed bool) uint32 {
	if hasCsumSeed {
		return csumSeed
	}
	return CRC32C(crc32cInit, uuid[:])
}

// SuperblockChecksum is s_checksum for a raw superblock: the CRC32C, from a
// seed of ^0, of the bytes preceding s_checksum (ext4_superblock_csum).
//
// It covers those bytes rather than the whole superblock with the field
// zeroed; the trailing zeros would be folded in. raw must hold at least
// SbOffChecksum bytes.
func SuperblockChecksum(raw []byte) uint32 {
	return CRC32C(crc32cInit, raw[:SbOffChecksum])
}

// GroupDescChecksum is bg_checksum for a raw group descriptor on a
// metadata_csum filesystem (ext4_group_desc_csum): the CRC32C of the group
// number, then the first descSize bytes of the descriptor with bg_checksum
// taken as zero, truncated to 16 bits.
//
// descSize is the filesystem's descriptor size: GroupDescSizeMin without
// 64BIT, s_desc_size with it. It is clamped to len(raw).
//
// The older GDT_CSUM flavour, a CRC-16 over the UUID, is not computed here.
func GroupDescChecksum(seed uint32, group uint32, raw []byte, descSize int) uint16 {
	if descSize > len(raw) {
		descSize = len(raw)
	}
	var g [4]byte
	binary.LittleEndian.PutUint32(g[:], group)
	crc := CRC32C(seed, g[:])
	if descSize <= GdOffChecksum {
		return uint16(CRC32C(crc, raw[:descSize]))
	}
	var zero [2]byte
	crc = CRC32C(crc, raw[:GdOffChecksum])
	crc = CRC32C(crc, zero[:])
	if descSize > GdOffChecksum+2 {
		crc = CRC32C(crc, raw[GdOffChecksum+2:descSize])
	}
	return uint16(crc)
}

// inodeSeed is the per-inode seed (ei->i_csum_seed) that an inode's own
// checksum, its extent tree blocks and its directory blocks start from: the
// metadata seed extended by the inode number and then its generation.
func inodeSeed(seed, ino, gen uint32) uint32 {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], ino)
	crc := CRC32C(seed, b[:])
	binary.LittleEndian.PutUint32(b[:], gen)
	return CRC32C(crc, b[:])
}

// InodeChecksum is the checksum of a raw inode (ext4_inode_csum): the CRC32C,
// from the inode's seed, of its first inodeSize bytes with i_checksum_lo, and
// i_checksum_hi where it exists, taken as zero.
//
// inodeSize is the filesystem's s_inode_size, not 128 + i_extra_isize: the
// whole on-disk slot is covered. It is clamped to len(raw). ino is the inode's
// number and gen its i_generation.
//
// The low 16 bits go in i_checksum_lo. When hasHi is true the inode is large
// enough to carry i_checksum_hi - i_extra_isize reaches past it - and hi goes
// there; otherwise only the low half is stored and hi is zero. A raw slice
// shorter than GoodOldInodeSize has no checksum and yields zeros.
func InodeChecksum(seed uint32, ino, gen uint32, raw []byte, inodeSize int) (lo uint16, hi uint16, hasHi bool) {
	if inodeSize > len(raw) {
		inodeSize = len(raw)
	}
	if inodeSize < GoodOldInodeSize {
		return 0, 0, false
	}
	hasHi = inodeSize >= inodeChecksumHiEnd &&
		GoodOldInodeSize+int(le16(raw, InodeOffExtraISize)) >= inodeChecksumHiEnd

	var zero [2]byte
	crc := inodeSeed(seed, ino, gen)
	crc = CRC32C(crc, raw[:InodeOffChecksumLo])
	crc = CRC32C(crc, zero[:])
	if !hasHi {
		crc = CRC32C(crc, raw[InodeOffChecksumLo+2:inodeSize])
		return uint16(crc), 0, false
	}
	crc = CRC32C(crc, raw[InodeOffChecksumLo+2:InodeOffChecksumHi])
	crc = CRC32C(crc, zero[:])
	crc = CRC32C(crc, raw[InodeOffChecksumHi+2:inodeSize])
	return uint16(crc), uint16(crc >> 16), true
}

// BitmapChecksum is the checksum of a block or inode bitmap
// (ext4_block_bitmap_csum_set, ext4_inode_bitmap_csum_set): the CRC32C of the
// bitmap from the metadata seed.
//
// bitmap must be the bitmap's meaningful bytes only - blocks per group / 8 or
// inodes per group / 8 - not the whole block holding it. The descriptor stores
// the low 16 bits, and the high 16 only when it is GroupDescSize64Bit wide.
func BitmapChecksum(seed uint32, bitmap []byte) uint32 {
	return CRC32C(seed, bitmap)
}

// ExtentTailChecksum is et_checksum for an extent tree block
// (ext4_extent_block_csum): the CRC32C, from the owning inode's seed, of the
// block up to its tail.
//
// The tail follows the last slot the header provides for, eh_max, not the last
// one in use, so the covered length is ExtentHeaderSize + ExtentEntrySize *
// eh_max. When that leaves no room for the ExtentTailSize-byte tail within
// block - the header is damaged - the result is 0.
func ExtentTailChecksum(seed, ino, gen uint32, block []byte) uint32 {
	off, ok := extentTailOffset(block)
	if !ok {
		return 0
	}
	return CRC32C(inodeSeed(seed, ino, gen), block[:off])
}

// extentTailOffset locates the tail of an extent tree block.
func extentTailOffset(block []byte) (int, bool) {
	if len(block) < ExtentHeaderSize {
		return 0, false
	}
	off := ExtentHeaderSize + ExtentEntrySize*int(le16(block, 4))
	if off+ExtentTailSize > len(block) {
		return 0, false
	}
	return off, true
}

// DirTailChecksum is det_checksum for a directory leaf block
// (ext4_dirblock_csum): the CRC32C, from the owning inode's seed, of the block
// up to the DirTailSize-byte tail record that ends it. block is the whole
// directory block.
func DirTailChecksum(seed, ino, gen uint32, block []byte) uint32 {
	if len(block) < DirTailSize {
		return 0
	}
	return CRC32C(inodeSeed(seed, ino, gen), block[:len(block)-DirTailSize])
}

// csumSeed is the value that seeds group descriptor and inode checksums.
func (fs *FS) csumSeed() uint32 {
	return MetadataSeed(fs.sb.UUID, fs.sb.ChecksumSeed, fs.sb.FeatureIncompat&IncompatCSumSeed != 0)
}

func (fs *FS) shouldValidateMetadataChecksums() bool {
	return (fs.sb.FeatureROCompat & ROCompatMetadataCsum) != 0
}

func (fs *FS) verifySuperblockChecksum(raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < SuperblockSize {
		return ErrUnsupportedLayout
	}

	stored := le32(raw, SbOffChecksum)
	calc := SuperblockChecksum(raw)
	if stored != calc {
		return fmt.Errorf("%w: superblock stored=0x%08x calc=0x%08x", ErrChecksumMismatch, stored, calc)
	}
	return nil
}

func (fs *FS) verifyGroupDescriptorChecksum(group uint32, raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < GroupDescSizeMin {
		return ErrUnsupportedLayout
	}

	stored := le16(raw, GdOffChecksum)
	descSize := int(fs.sb.GroupDescSize)
	if descSize < GroupDescSizeMin {
		descSize = GroupDescSizeMin
	}

	calc := GroupDescChecksum(fs.csumSeed(), group, raw, descSize)
	if stored != calc {
		return fmt.Errorf("%w: group=%d stored=0x%04x calc=0x%04x", ErrChecksumMismatch, group, stored, calc)
	}
	return nil
}

func (fs *FS) verifyInodeChecksum(inodeNum uint32, raw []byte) error {
	if !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(raw) < GoodOldInodeSize {
		return nil
	}

	lo, hi, hasHi := InodeChecksum(fs.csumSeed(), inodeNum, le32(raw, InodeOffGeneration), raw, len(raw))
	storedLo := le16(raw, InodeOffChecksumLo)
	if hasHi {
		stored := uint32(storedLo) | uint32(le16(raw, InodeOffChecksumHi))<<16
		calc := uint32(lo) | uint32(hi)<<16
		if calc != stored {
			return fmt.Errorf("%w: inode=%d stored=0x%08x calc=0x%08x", ErrChecksumMismatch, inodeNum, stored, calc)
		}
		return nil
	}

	if lo != storedLo {
		return fmt.Errorf("%w: inode=%d stored=0x%04x calc=0x%04x", ErrChecksumMismatch, inodeNum, storedLo, lo)
	}
	return nil
}

// bitmapChecksumWidth reports how many bits of a bitmap checksum are stored.
// The high half of the field exists only in a 64-bit group descriptor.
func (fs *FS) bitmapChecksumWidth() uint32 {
	if fs.sb.GroupDescSize >= GroupDescSize64Bit {
		return 32
	}
	return 16
}

// verifyBitmapChecksum compares a stored bitmap checksum against the data.
func (fs *FS) verifyBitmapChecksum(group uint32, stored uint32, data []byte, kind string) error {
	calc := BitmapChecksum(fs.csumSeed(), data)
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
