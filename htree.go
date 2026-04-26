package libext

import (
	"encoding/binary"
	"errors"
)

const (
	htreeMaxDepth = 4
)

// HTreeValidationError represents an HTree structure validation error.
type HTreeValidationError struct {
	Reason string
}

func (e HTreeValidationError) Error() string {
	return "htree validation error: " + e.Reason
}

// IsHTreeDirectory checks if an inode uses HTree directory indexing.
func (fs *FS) IsHTreeDirectory(inode *Inode) bool {
	// HTree requires DIR_INDEX feature and is only for directories
	if !inode.IsDirectory {
		return false
	}
	if (fs.sb.FeatureCompat & featureCompatDirIndex) == 0 {
		return false
	}
	// Directory size should be at least 8KB for HTree
	if inode.Size < 8192 {
		return false
	}
	return true
}

// DetectHTreeUsage checks if a directory uses HTree indexing by examining the root block.
// Returns true if HTree structure is detected, false if it's a linear directory or error.
func (fs *FS) DetectHTreeUsage(inode *Inode) (bool, error) {
	if !inode.IsDirectory {
		return false, nil
	}
	if (fs.sb.FeatureCompat & featureCompatDirIndex) == 0 {
		return false, nil
	}
	if inode.Size < 8192 {
		return false, nil
	}

	// Read first block and check for HTree root node signature
	data, err := fs.readInodeData(*inode)
	if err != nil || len(data) < 32 {
		return false, err
	}

	// Check for valid HTree root node structure
	// Characteristics:
	// - dot entry starts at offset 0
	// - info_length at offset 18 (should be 4-32)
	// - indirect level at offset 19 (should be 1-4)
	// - hash_version at offset 21 (should be 0-2)

	dotRecLen := binary.LittleEndian.Uint16(data[0:2])
	infoLength := data[18]
	indirect := data[19]
	hashVersion := data[21]

	// If the structure looks like HTree root node, return true
	isHTree := dotRecLen >= 12 && dotRecLen <= 32 &&
		infoLength >= 4 && infoLength <= 32 &&
		indirect >= 1 && indirect <= htreeMaxDepth &&
		hashVersion <= 2

	return isHTree, nil
}

// ValidateHTreeRootNode performs basic validation on an HTree root node.
func ValidateHTreeRootNode(data []byte) error {
	if len(data) < 32 {
		return errors.New("htree block too small for root node header")
	}

	// Extract root node structure fields
	dotRecLen := binary.LittleEndian.Uint16(data[0:2])
	if dotRecLen < 12 || dotRecLen > 32 {
		return HTreeValidationError{
			Reason: "invalid dot entry rec_len: " + string(rune(dotRecLen)),
		}
	}

	// InfoLength should be reasonable (typically 8)
	infoLength := data[18]
	if infoLength < 4 || infoLength > 32 {
		return HTreeValidationError{
			Reason: "invalid info length",
		}
	}

	// Indirect depth should not exceed reasonable limits
	indirect := data[19]
	if indirect < 1 || indirect > htreeMaxDepth {
		return HTreeValidationError{
			Reason: "tree depth out of valid range",
		}
	}

	// HashVersion should be 0, 1, or 2
	hashVersion := data[21]
	if hashVersion > 2 {
		return HTreeValidationError{
			Reason: "unknown hash version",
		}
	}

	return nil
}

// EnhancedListDir lists directory entries, handling both linear and HTree-indexed directories.
func (fs *FS) EnhancedListDir(inodeNum uint32) ([]DirEntry, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	if !inode.IsDirectory {
		return nil, ErrNotDirectory
	}

	// Check if this is an HTree directory
	isHTree, err := fs.DetectHTreeUsage(&inode)
	if err != nil {
		// If we can't detect HTree, just do linear read
		isHTree = false
	}

	if isHTree {
		// For HTree directories, read all data and parse with validation
		data, err := fs.readInodeData(inode)
		if err != nil {
			return nil, err
		}

		// Validate the HTree root node structure
		if err := ValidateHTreeRootNode(data); err != nil {
			// If validation fails but it looks like HTree, return error
			return nil, err
		}

		// Parse all directory entries (HTree indexes but same linear format for entries)
		return fs.parseDirEntries(data)
	}

	// Linear directory - use existing logic
	return fs.ListDir(inodeNum)
}
