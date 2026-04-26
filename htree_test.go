package libext

import (
	"bytes"
	"testing"
)

// TestHTreeValidationPass tests valid HTree root node structures.
func TestHTreeValidationPass(t *testing.T) {
	// Create a valid HTree root node structure
	data := make([]byte, 512)

	// dot entry (rec_len=12, name_len=1, file_type=2 for directory)
	data[0] = 12
	data[1] = 0
	data[6] = 1 // name_len
	data[7] = 2 // file_type (directory)

	// inode number for dot (typically 2 for root, but can be directory's own inode)
	data[8] = 2
	data[9] = 0
	data[10] = 0
	data[11] = 0

	// dotdot entry
	data[12] = 0 // rec_len low byte (will be set properly below)
	data[13] = 0 // rec_len high byte
	data[14] = 0
	data[15] = 0

	// HTree header starts at offset 16 (after dot and dotdot entries)
	// reserved (offset 16)
	data[16] = 0
	data[17] = 0

	// info_length (offset 18) - should be 4-32
	data[18] = 8

	// indirect (offset 19) - should be 1-4
	data[19] = 1

	// unused (offset 20)
	data[20] = 0

	// hash_version (offset 21) - should be 0-2
	data[21] = 0

	// Test validation
	err := ValidateHTreeRootNode(data)
	if err != nil {
		t.Errorf("Expected valid HTree root node, got error: %v", err)
	}
}

// TestHTreeValidationInvalidRecLen tests invalid dot rec_len.
func TestHTreeValidationInvalidRecLen(t *testing.T) {
	data := make([]byte, 512)

	// Invalid rec_len (too small)
	data[0] = 4
	data[1] = 0

	err := ValidateHTreeRootNode(data)
	if err == nil {
		t.Error("Expected validation error for invalid rec_len, got nil")
	}
}

// TestHTreeValidationInvalidInfoLength tests invalid info_length.
func TestHTreeValidationInvalidInfoLength(t *testing.T) {
	data := make([]byte, 512)

	// Valid rec_len
	data[0] = 12
	data[1] = 0
	data[6] = 1
	data[7] = 2

	// Invalid info_length (too large)
	data[18] = 64

	// Valid indirect
	data[19] = 1
	data[21] = 0

	err := ValidateHTreeRootNode(data)
	if err == nil {
		t.Error("Expected validation error for invalid info_length, got nil")
	}
}

// TestHTreeValidationInvalidIndirect tests invalid indirect level.
func TestHTreeValidationInvalidIndirect(t *testing.T) {
	data := make([]byte, 512)

	// Valid rec_len, info_length
	data[0] = 12
	data[1] = 0
	data[6] = 1
	data[7] = 2
	data[18] = 8

	// Invalid indirect (too large)
	data[19] = 5
	data[21] = 0

	err := ValidateHTreeRootNode(data)
	if err == nil {
		t.Error("Expected validation error for invalid indirect level, got nil")
	}
}

// TestHTreeValidationInvalidHashVersion tests invalid hash version.
func TestHTreeValidationInvalidHashVersion(t *testing.T) {
	data := make([]byte, 512)

	// Valid rec_len, info_length, indirect
	data[0] = 12
	data[1] = 0
	data[6] = 1
	data[7] = 2
	data[18] = 8
	data[19] = 1

	// Invalid hash_version
	data[21] = 5

	err := ValidateHTreeRootNode(data)
	if err == nil {
		t.Error("Expected validation error for invalid hash version, got nil")
	}
}

// TestIsHTreeDirectory tests directory HTree detection.
func TestIsHTreeDirectory(t *testing.T) {
	// Create a mock FS with DIR_INDEX feature
	fs := &FS{
		sb: Superblock{
			FeatureCompat: featureCompatDirIndex,
			BlockSize:     4096,
		},
	}

	// Test case 1: Directory with HTree feature and large size
	inode := &Inode{
		IsDirectory: true,
		Size:        16384, // > 8192
	}
	if !fs.IsHTreeDirectory(inode) {
		t.Error("Expected IsHTreeDirectory to return true for large directory")
	}

	// Test case 2: Directory too small for HTree
	inode.Size = 4096
	if fs.IsHTreeDirectory(inode) {
		t.Error("Expected IsHTreeDirectory to return false for small directory")
	}

	// Test case 3: Not a directory
	inode.IsDirectory = false
	inode.Size = 16384
	if fs.IsHTreeDirectory(inode) {
		t.Error("Expected IsHTreeDirectory to return false for non-directory")
	}

	// Test case 4: DIR_INDEX feature not set
	inode.IsDirectory = true
	fs.sb.FeatureCompat = 0
	if fs.IsHTreeDirectory(inode) {
		t.Error("Expected IsHTreeDirectory to return false when DIR_INDEX not set")
	}
}

// TestDetectHTreeUsage tests HTree usage detection.
func TestDetectHTreeUsage(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureCompat: featureCompatDirIndex,
			BlockSize:     4096,
		},
		r:         bytes.NewReader(make([]byte, 8192*10)),
		imageSize: uint64(8192 * 10),
	}

	// Create a synthetic directory inode with HTree-like data
	inode := &Inode{
		IsDirectory: true,
		Size:        16384,
	}

	// For this test, we'd need to mock readInodeData, so we'll skip the
	// full integration test and just verify the logic for detection
	// when data is unavailable

	// Test case 1: Not a directory
	inode.IsDirectory = false
	isHTree, err := fs.DetectHTreeUsage(inode)
	if err != nil || isHTree {
		t.Error("Expected false for non-directory inode")
	}

	// Test case 2: DIR_INDEX feature not set
	inode.IsDirectory = true
	fs.sb.FeatureCompat = 0
	isHTree, err = fs.DetectHTreeUsage(inode)
	if err != nil || isHTree {
		t.Error("Expected false when DIR_INDEX feature not set")
	}

	// Test case 3: Directory too small
	inode.Size = 4096
	fs.sb.FeatureCompat = featureCompatDirIndex
	isHTree, err = fs.DetectHTreeUsage(inode)
	if err != nil || isHTree {
		t.Error("Expected false for small directory")
	}
}

// TestHTreeValidationErrorMessage tests error message formatting.
func TestHTreeValidationErrorMessage(t *testing.T) {
	err := HTreeValidationError{Reason: "test reason"}
	expected := "htree validation error: test reason"
	if err.Error() != expected {
		t.Errorf("Expected error message '%s', got '%s'", expected, err.Error())
	}
}
