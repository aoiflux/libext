package libext

import (
	"testing"
)

// These tests previously asserted that verification returned no error for
// arbitrary data, because the implementation computed a CRC and discarded it.
// That pinned the stub rather than the behaviour. They now stamp the checksum a
// filesystem would store and check that verification accepts it and rejects a
// tampered bitmap.

// bitmapFixture builds an FS with one group and a known checksum seed.
func bitmapFixture(t testing.TB, bitmap []byte, kind string) *FS {
	t.Helper()

	fs := &FS{
		sb: Superblock{
			FeatureROCompat: featureRoCompatMetadataCS,
			UUID:            [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			GroupDescSize:   32,
		},
		groups: []GroupDescriptor{{BlockBitmapBlock: 1, InodeBitmapBlock: 2}},
	}

	csum := extCRC32C(fs.csumSeed(), bitmap) & 0xFFFF
	switch kind {
	case "block":
		fs.groups[0].BlockBitmapChecksum = csum
	case "inode":
		fs.groups[0].InodeBitmapChecksum = csum
	}
	return fs
}

func TestVerifyBlockBitmapChecksum(t *testing.T) {
	bitmap := make([]byte, 512)
	bitmap[0] = 0xFF

	fs := bitmapFixture(t, bitmap, "block")

	if err := fs.VerifyBlockBitmapChecksum(0, bitmap); err != nil {
		t.Errorf("correctly stamped block bitmap failed verification: %v", err)
	}

	bitmap[7] ^= 0x01
	if err := fs.VerifyBlockBitmapChecksum(0, bitmap); err == nil {
		t.Error("tampered block bitmap passed verification")
	}
}

func TestVerifyInodeBitmapChecksum(t *testing.T) {
	bitmap := make([]byte, 512)
	bitmap[0] = 0xAA

	fs := bitmapFixture(t, bitmap, "inode")

	if err := fs.VerifyInodeBitmapChecksum(0, bitmap); err != nil {
		t.Errorf("correctly stamped inode bitmap failed verification: %v", err)
	}

	bitmap[0] = 0xAB
	if err := fs.VerifyInodeBitmapChecksum(0, bitmap); err == nil {
		t.Error("tampered inode bitmap passed verification")
	}
}

// TestVerifyBitmapChecksumSkipsUninitGroup covers the case that keeps a scan
// honest: an uninitialised group has no bitmap to verify, so a mismatch there
// is meaningless rather than evidence of corruption.
func TestVerifyBitmapChecksumSkipsUninitGroup(t *testing.T) {
	fs := &FS{
		sb:     Superblock{FeatureROCompat: featureRoCompatMetadataCS, GroupDescSize: 32},
		groups: []GroupDescriptor{{Flags: GroupBlockUninit | GroupInodeUninit}},
	}

	bitmap := make([]byte, 512)
	bitmap[3] = 0x5A

	if err := fs.VerifyBlockBitmapChecksum(0, bitmap); err != nil {
		t.Errorf("uninitialised block group reported a mismatch: %v", err)
	}
	if err := fs.VerifyInodeBitmapChecksum(0, bitmap); err != nil {
		t.Errorf("uninitialised inode group reported a mismatch: %v", err)
	}
}

func TestVerifyBitmapChecksumDisabled(t *testing.T) {
	fs := &FS{
		sb:     Superblock{FeatureROCompat: 0}, // METADATA_CSUM not set
		groups: []GroupDescriptor{{BlockBitmapBlock: 1}},
	}

	if err := fs.VerifyBlockBitmapChecksum(0, make([]byte, 512)); err != nil {
		t.Errorf("VerifyBlockBitmapChecksum with the feature disabled: %v", err)
	}
}

func TestVerifyBitmapChecksumInvalidGroup(t *testing.T) {
	fs := &FS{
		sb:     Superblock{FeatureROCompat: featureRoCompatMetadataCS, GroupDescSize: 32},
		groups: []GroupDescriptor{{BlockBitmapBlock: 1}},
	}

	if err := fs.VerifyBlockBitmapChecksum(99, make([]byte, 512)); err == nil {
		t.Error("expected an error for an out-of-range group")
	}
}
