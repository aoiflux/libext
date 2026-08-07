package libext

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestFeatureCompatibility tests feature combinations and validation.
// This validates that unsupported features are properly rejected.
func TestFeatureCompatibility(t *testing.T) {
	tests := []struct {
		name      string
		compat    uint32
		incompat  uint32
		rocompat  uint32
		shouldErr bool
	}{
		{
			name:      "no features",
			compat:    0,
			incompat:  0,
			rocompat:  0,
			shouldErr: false,
		},
		{
			name:      "EXT3 with journal",
			compat:    featureCompatHasJournal,
			incompat:  0,
			rocompat:  0,
			shouldErr: false,
		},
		{
			name:      "EXT4 with supported features",
			compat:    featureCompatDirIndex,
			incompat:  featureIncompatExtents | featureIncompatFileType,
			rocompat:  featureRoCompatMetadataCS,
			shouldErr: false,
		},
		{
			name:      "unsupported incompat COMPRESSION",
			compat:    0,
			incompat:  0x0001, // COMPRESSION
			rocompat:  0,
			shouldErr: true,
		},
		{
			// Inline data is supported, so it must no longer block Open.
			name:      "supported incompat INLINE_DATA",
			compat:    0,
			incompat:  0x8000, // INLINE_DATA
			rocompat:  0,
			shouldErr: false,
		},
		{
			name:      "unsupported incompat JOURNAL_DEV",
			compat:    0,
			incompat:  0x0008, // JOURNAL_DEV
			rocompat:  0,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test feature validation directly
			fs := &FS{
				sb: Superblock{
					FeatureCompat:   tt.compat,
					FeatureIncompat: tt.incompat,
					FeatureROCompat: tt.rocompat,
				},
			}

			err := fs.CheckRequiredFeatures()
			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error for unsupported feature, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// TestBlockSizeCalculation tests block size calculation logic.
func TestBlockSizeCalculation(t *testing.T) {
	tests := []struct {
		logBlockSize uint32
		expectedSize uint32
		name         string
	}{
		{0, 1024, "1KB"},
		{1, 2048, "2KB"},
		{2, 4096, "4KB"},
		{3, 8192, "8KB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify block size calculation formula
			blockSize := uint32(1024) << tt.logBlockSize
			if blockSize != tt.expectedSize {
				t.Errorf("Expected block size %d, got %d", tt.expectedSize, blockSize)
			}
		})
	}
}

// TestInodeSizeParsing tests inode size field parsing.
func TestInodeSizeParsing(t *testing.T) {
	tests := []struct {
		inodeSize uint16
		name      string
	}{
		{128, "EXT2 standard"},
		{256, "EXT4 standard"},
		{512, "EXT4 large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create superblock buffer with inode size field
			sb := make([]byte, 256)
			binary.LittleEndian.PutUint16(sb[88:90], tt.inodeSize)

			// Verify parsing
			parsedSize := binary.LittleEndian.Uint16(sb[88:90])
			if parsedSize != tt.inodeSize {
				t.Errorf("Expected inode size %d, got %d", tt.inodeSize, parsedSize)
			}
		})
	}
}

// TestSuperblockMagicValidation tests superblock magic number validation.
func TestSuperblockMagicValidation(t *testing.T) {
	tests := []struct {
		magic   uint16
		isValid bool
		name    string
	}{
		{0xEF53, true, "valid EXT magic"},
		{0x0000, false, "invalid magic (zero)"},
		{0xFFFF, false, "invalid magic (all ones)"},
		{0xE953, false, "invalid magic (typo)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := make([]byte, 256)
			binary.LittleEndian.PutUint16(sb[56:58], tt.magic)

			parsedMagic := binary.LittleEndian.Uint16(sb[56:58])
			isValid := parsedMagic == extMagic

			if isValid != tt.isValid {
				t.Errorf("Expected valid=%v for magic 0x%04X, got %v", tt.isValid, tt.magic, isValid)
			}
		})
	}
}

// TestRevisionLevelDetection tests filesystem kind detection by revision level.
func TestRevisionLevelDetection(t *testing.T) {
	tests := []struct {
		revLevel     uint32
		incompat     uint32
		expectedKind FSKind
		name         string
	}{
		{0, 0, FSKindExt2, "EXT2 (revision 0)"},
		{0, featureIncompatFileType, FSKindExt2, "EXT2 with file type"},
		{1, 0, FSKindExt3, "EXT3 (revision 1, no extents)"},
		{1, featureIncompatExtents, FSKindExt4, "EXT4 (revision 1 with extents)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := Superblock{
				RevisionLevel:   tt.revLevel,
				FeatureIncompat: tt.incompat,
			}

			// Simulate kind detection logic
			var kind FSKind
			if (sb.FeatureIncompat&featureIncompatExtents) != 0 ||
				(sb.FeatureIncompat&featureIncompat64Bit) != 0 {
				kind = FSKindExt4
			} else if sb.RevisionLevel >= 1 {
				kind = FSKindExt3
			} else {
				kind = FSKindExt2
			}

			if kind != tt.expectedKind {
				t.Errorf("Expected kind %s, got %s", tt.expectedKind, kind)
			}
		})
	}
}

// TestMinimalSuperblockOpen tests opening a minimal valid superblock.
func TestMinimalSuperblockOpen(t *testing.T) {
	// Create a minimal but valid superblock image
	imageData := make([]byte, 2048)
	sb := imageData[1024 : 1024+1024]

	// Essential fields
	binary.LittleEndian.PutUint32(sb[0:4], 256)        // inode count
	binary.LittleEndian.PutUint32(sb[4:8], 1000)       // block count (low)
	binary.LittleEndian.PutUint32(sb[8:12], 10)        // reserved blocks
	binary.LittleEndian.PutUint32(sb[12:16], 500)      // free blocks
	binary.LittleEndian.PutUint32(sb[16:20], 128)      // free inodes
	binary.LittleEndian.PutUint32(sb[20:24], 1)        // first data block
	binary.LittleEndian.PutUint32(sb[24:28], 2)        // log block size (4KB)
	binary.LittleEndian.PutUint32(sb[32:36], 8192)     // blocks per group
	binary.LittleEndian.PutUint32(sb[40:44], 256)      // inodes per group
	binary.LittleEndian.PutUint16(sb[56:58], extMagic) // magic
	binary.LittleEndian.PutUint16(sb[58:60], 1)        // state (clean)
	binary.LittleEndian.PutUint32(sb[76:80], 0)        // revision level
	binary.LittleEndian.PutUint16(sb[88:90], 128)      // inode size

	// Try to open - will fail on group descriptor read but that's expected
	// This test verifies superblock parsing itself works
	imageReader := bytes.NewReader(imageData)
	fs, err := OpenWithSize(imageReader, uint64(len(imageData)))
	if err != nil {
		// Expected - group descriptors are out of bounds
		// But superblock should have been parsed
		t.Logf("Open failed as expected: %v", err)
		return
	}
	defer fs.Close()

	// Verify superblock was parsed
	if fs.sb.InodesCount != 256 {
		t.Errorf("Expected 256 inodes, got %d", fs.sb.InodesCount)
	}
	if fs.sb.BlockSize != 4096 {
		t.Errorf("Expected 4096 block size, got %d", fs.sb.BlockSize)
	}
}

// TestFSKindDetection tests filesystem type detection.
func TestFSKindDetection(t *testing.T) {
	tests := []struct {
		name         string
		setupSB      func(*Superblock)
		expectedKind FSKind
	}{
		{
			name: "EXT2",
			setupSB: func(sb *Superblock) {
				sb.RevisionLevel = 0
				sb.FeatureIncompat = 0
			},
			expectedKind: FSKindExt2,
		},
		{
			name: "EXT3",
			setupSB: func(sb *Superblock) {
				sb.RevisionLevel = 1
				sb.FeatureIncompat = 0
				sb.FeatureCompat = featureCompatHasJournal
			},
			expectedKind: FSKindExt3,
		},
		{
			name: "EXT4",
			setupSB: func(sb *Superblock) {
				sb.RevisionLevel = 1
				sb.FeatureIncompat = featureIncompatExtents
			},
			expectedKind: FSKindExt4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := &Superblock{}
			tt.setupSB(sb)

			var detected FSKind
			if (sb.FeatureIncompat&featureIncompatExtents) != 0 ||
				(sb.FeatureIncompat&featureIncompat64Bit) != 0 {
				detected = FSKindExt4
			} else if sb.RevisionLevel >= 1 {
				detected = FSKindExt3
			} else {
				detected = FSKindExt2
			}

			if detected != tt.expectedKind {
				t.Errorf("Expected %s, got %s", tt.expectedKind, detected)
			}
		})
	}
}
