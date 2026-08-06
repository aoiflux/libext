package libext

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestVerifyInodeChecksumLow16(t *testing.T) {
	fs := &FS{sb: Superblock{FeatureROCompat: featureRoCompatMetadataCS}}
	copy(fs.sb.UUID[:], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	inodeNum := uint32(42)
	raw := make([]byte, 128)
	for i := range raw {
		raw[i] = byte((i * 3) & 0xFF)
	}
	binary.LittleEndian.PutUint32(raw[0x64:0x68], 0x11223344) // i_generation

	calc := inodeChecksumForTest(fs.sb.UUID, inodeNum, raw, false)
	binary.LittleEndian.PutUint16(raw[0x7C:0x7E], uint16(calc&0xFFFF))

	if err := fs.verifyInodeChecksum(inodeNum, raw); err != nil {
		t.Fatalf("expected checksum to validate, got %v", err)
	}

	raw[0x10] ^= 0x7F
	err := fs.verifyInodeChecksum(inodeNum, raw)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch after tamper, got %v", err)
	}
}

func TestVerifyInodeChecksumHigh32(t *testing.T) {
	fs := &FS{sb: Superblock{FeatureROCompat: featureRoCompatMetadataCS}}
	copy(fs.sb.UUID[:], []byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1})

	inodeNum := uint32(1337)
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = byte((i*5 + 1) & 0xFF)
	}
	binary.LittleEndian.PutUint32(raw[0x64:0x68], 0xA1B2C3D4) // i_generation
	binary.LittleEndian.PutUint16(raw[0x80:0x82], 4)          // i_extra_isize so hi checksum exists

	calc := inodeChecksumForTest(fs.sb.UUID, inodeNum, raw, true)
	binary.LittleEndian.PutUint16(raw[0x7C:0x7E], uint16(calc&0xFFFF))
	binary.LittleEndian.PutUint16(raw[0x82:0x84], uint16(calc>>16))

	if err := fs.verifyInodeChecksum(inodeNum, raw); err != nil {
		t.Fatalf("expected 32-bit inode checksum to validate, got %v", err)
	}

	binary.LittleEndian.PutUint16(raw[0x82:0x84], 0)
	err := fs.verifyInodeChecksum(inodeNum, raw)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch when checksum hi is wrong, got %v", err)
	}
}

func TestVerifyInodeChecksumDisabled(t *testing.T) {
	fs := &FS{sb: Superblock{FeatureROCompat: 0}}
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = 0xFF
	}
	if err := fs.verifyInodeChecksum(5, raw); err != nil {
		t.Fatalf("expected checksum verification disabled, got %v", err)
	}
}

// inodeChecksumForTest stamps fixtures with the checksum ext4 actually stores.
//
// This helper previously used hash/crc32 directly, which applies a final
// complement that Linux's crc32c does not. That made these tests agree with the
// implementation's own mistake — both were off by the same complement — so the
// suite passed while no real ext4 image ever verified. The ground truth now
// lives in checksum_vector_test.go, against a descriptor written by mke2fs.
func inodeChecksumForTest(uuid [16]byte, inodeNum uint32, raw []byte, hasHi bool) uint32 {
	buf := make([]byte, len(raw))
	copy(buf, raw)
	buf[0x7C] = 0
	buf[0x7D] = 0
	if hasHi && len(buf) >= 0x84 {
		buf[0x82] = 0
		buf[0x83] = 0
	}

	inum := make([]byte, 4)
	binary.LittleEndian.PutUint32(inum, inodeNum)
	gen := make([]byte, 4)
	binary.LittleEndian.PutUint32(gen, binary.LittleEndian.Uint32(raw[0x64:0x68]))

	seed := extCRC32C(crc32cInit, uuid[:])
	crc := extCRC32C(seed, inum)
	crc = extCRC32C(crc, gen)
	return extCRC32C(crc, buf)
}
