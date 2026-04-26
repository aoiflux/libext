package libext

import (
	"testing"
)

// TestVerifyBlockBitmapChecksum tests block bitmap checksum validation.
func TestVerifyBlockBitmapChecksum(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: featureRoCompatMetadataCS,
			UUID:            [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		},
		groups: []GroupDescriptor{
			{
				BlockBitmapBlock: 1,
				Flags:            0,
			},
		},
	}

	blockBitmap := make([]byte, 512)
	blockBitmap[0] = 0xFF // Set some bits

	// Should not error (actual checksum validation is stubbed)
	err := fs.VerifyBlockBitmapChecksum(0, blockBitmap)
	if err != nil {
		t.Errorf("VerifyBlockBitmapChecksum: unexpected error %v", err)
	}
}

// TestVerifyInodeBitmapChecksum tests inode bitmap checksum validation.
func TestVerifyInodeBitmapChecksum(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: featureRoCompatMetadataCS,
			UUID:            [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		},
		groups: []GroupDescriptor{
			{
				InodeBitmapBlock: 2,
				Flags:            0,
			},
		},
	}

	inodeBitmap := make([]byte, 512)
	inodeBitmap[0] = 0xAA // Set alternating bits

	// Should not error (actual checksum validation is stubbed)
	err := fs.VerifyInodeBitmapChecksum(0, inodeBitmap)
	if err != nil {
		t.Errorf("VerifyInodeBitmapChecksum: unexpected error %v", err)
	}
}

// TestVerifyBitmapChecksumDisabled tests bitmap checksum when feature is disabled.
func TestVerifyBitmapChecksumDisabled(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: 0, // METADATA_CSUM not set
		},
		groups: []GroupDescriptor{
			{
				BlockBitmapBlock: 1,
			},
		},
	}

	blockBitmap := make([]byte, 512)

	// Should not error when feature is disabled
	err := fs.VerifyBlockBitmapChecksum(0, blockBitmap)
	if err != nil {
		t.Errorf("VerifyBlockBitmapChecksum with disabled feature: unexpected error %v", err)
	}
}

// TestVerifyBitmapChecksumInvalidGroup tests bitmap checksum with invalid group.
func TestVerifyBitmapChecksumInvalidGroup(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: featureRoCompatMetadataCS,
		},
		groups: []GroupDescriptor{
			{BlockBitmapBlock: 1},
		},
	}

	blockBitmap := make([]byte, 512)

	// Should error for invalid group
	err := fs.VerifyBlockBitmapChecksum(99, blockBitmap)
	if err == nil {
		t.Error("VerifyBlockBitmapChecksum: expected error for invalid group, got nil")
	}
}
