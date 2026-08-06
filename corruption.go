package libext

import (
	"fmt"
)

// CorruptionSeverity indicates the severity of a detected corruption.
type CorruptionSeverity int

const (
	SeverityInfo CorruptionSeverity = iota
	SeverityWarning
	SeverityCritical
)

// CorruptionReport documents a detected corruption or edge case.
type CorruptionReport struct {
	Severity   CorruptionSeverity
	Location   string
	Issue      string
	Suggestion string
}

// ValidateSuperblockIntegrity checks superblock for obvious corruptions.
func (fs *FS) ValidateSuperblockIntegrity() []CorruptionReport {
	var reports []CorruptionReport

	// Check block size range (must be 1K, 2K, 4K, or 8K)
	blockSize := fs.sb.BlockSize
	if blockSize == 0 || blockSize > 8192 || (blockSize&(blockSize-1) != 0) {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   "superblock.block_size",
			Issue:      fmt.Sprintf("Invalid block size: %d", blockSize),
			Suggestion: "Block size must be power of 2 between 1024 and 8192",
		})
	}

	// Check inode size (typically 128 for ext2/3, 256 for ext4)
	inodeSize := fs.sb.InodeSize
	if inodeSize == 0 || inodeSize < 128 || inodeSize > 4096 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityWarning,
			Location:   "superblock.inode_size",
			Issue:      fmt.Sprintf("Unusual inode size: %d", inodeSize),
			Suggestion: "Typical inode size is 128 or 256 bytes",
		})
	}

	// Check inodes/group ratio (sanity check)
	if fs.sb.InodesPerGroup > fs.sb.BlocksPerGroup*blockSize/128 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityWarning,
			Location:   "superblock.inodes_per_group",
			Issue:      "Inodes per group exceeds theoretical maximum",
			Suggestion: "Check if inode table allocation is correctly sized",
		})
	}

	// Check first data block (should be 0 for 4K blocks, 1 for smaller)
	expectedFirstBlock := uint32(1)
	if blockSize >= 4096 {
		expectedFirstBlock = 0
	}
	if fs.sb.FirstDataBlock != expectedFirstBlock && fs.sb.FirstDataBlock > 2 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityInfo,
			Location:   "superblock.first_data_block",
			Issue:      fmt.Sprintf("Unexpected first data block: %d", fs.sb.FirstDataBlock),
			Suggestion: fmt.Sprintf("Typically %d for block size %d", expectedFirstBlock, blockSize),
		})
	}

	// Check reserved blocks (should be small percentage)
	if fs.sb.BlocksCount > 0 {
		reservedPercent := (fs.sb.ReservedBlocks * 100) / fs.sb.BlocksCount
		if reservedPercent > 50 {
			reports = append(reports, CorruptionReport{
				Severity:   SeverityWarning,
				Location:   "superblock.reserved_blocks",
				Issue:      fmt.Sprintf("Unusually high reserved blocks: %d%%", reservedPercent),
				Suggestion: "Reserved blocks are typically 5%% of total blocks",
			})
		}
	}

	return reports
}

// ValidateInodeIntegrity checks an inode for corruption patterns.
func (fs *FS) ValidateInodeIntegrity(inode *Inode) []CorruptionReport {
	var reports []CorruptionReport

	// Check size consistency
	if inode.IsDirectory {
		// Directory size should be multiple of block size
		if inode.Size%uint64(fs.sb.BlockSize) != 0 {
			reports = append(reports, CorruptionReport{
				Severity:   SeverityInfo,
				Location:   fmt.Sprintf("inode %d size", inode.Number),
				Issue:      "Directory size not aligned to block size",
				Suggestion: "Directory entries should span whole blocks",
			})
		}
	} else if inode.IsRegular {
		// Check for sparse file sanity
		if inode.Blocks512 == 0 && inode.Size > 0 {
			reports = append(reports, CorruptionReport{
				Severity:   SeverityInfo,
				Location:   fmt.Sprintf("inode %d blocks", inode.Number),
				Issue:      "File with size but no allocated blocks (sparse/inline)",
				Suggestion: "Verify file actually uses inline data or sparse blocks",
			})
		}

		// Check block count sanity for non-sparse files
		if inode.Blocks512 > 0 {
			minBlocks := (inode.Size + uint64(fs.sb.BlockSize) - 1) / uint64(fs.sb.BlockSize)

			// Blocks512 is in 512-byte units, convert for comparison
			blocksInFileSize := inode.Blocks512 * 512 / uint64(fs.sb.BlockSize)

			if blocksInFileSize < minBlocks {
				reports = append(reports, CorruptionReport{
					Severity:   SeverityWarning,
					Location:   fmt.Sprintf("inode %d blocks", inode.Number),
					Issue:      fmt.Sprintf("Allocated blocks (%d) less than needed for size (%d)", blocksInFileSize, minBlocks),
					Suggestion: "File may be truncated or corrupted",
				})
			}
		}
	}

	// Check link count for corruption
	if inode.LinksCount == 0 && inode.Number != 0 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   fmt.Sprintf("inode %d links_count", inode.Number),
			Issue:      "Inode has zero link count (should be unreachable)",
			Suggestion: "May indicate filesystem corruption or orphaned inode",
		})
	}

	// Check generation field for corruption
	if inode.Generation == 0 && fs.sb.RevisionLevel >= 1 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityInfo,
			Location:   fmt.Sprintf("inode %d generation", inode.Number),
			Issue:      "Generation field is zero",
			Suggestion: "May indicate old filesystem or corrupted generation",
		})
	}

	return reports
}

// ValidateGroupDescriptorIntegrity checks a group descriptor for issues.
func (fs *FS) ValidateGroupDescriptorIntegrity(groupNum uint32, gd *GroupDescriptor) []CorruptionReport {
	var reports []CorruptionReport

	// Check block bitmap block exists and is valid
	if gd.BlockBitmapBlock == 0 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   fmt.Sprintf("group %d block_bitmap", groupNum),
			Issue:      "Block bitmap block number is zero",
			Suggestion: "Every group should have a block bitmap",
		})
	} else if gd.BlockBitmapBlock >= uint64(fs.sb.BlocksCount) {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   fmt.Sprintf("group %d block_bitmap", groupNum),
			Issue:      fmt.Sprintf("Block bitmap number %d exceeds total blocks %d", gd.BlockBitmapBlock, fs.sb.BlocksCount),
			Suggestion: "Block bitmap block number out of range",
		})
	}

	// Check inode bitmap block exists and is valid
	if gd.InodeBitmapBlock == 0 {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   fmt.Sprintf("group %d inode_bitmap", groupNum),
			Issue:      "Inode bitmap block number is zero",
			Suggestion: "Every group should have an inode bitmap",
		})
	} else if gd.InodeBitmapBlock >= uint64(fs.sb.BlocksCount) {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityCritical,
			Location:   fmt.Sprintf("group %d inode_bitmap", groupNum),
			Issue:      fmt.Sprintf("Inode bitmap number %d exceeds total blocks %d", gd.InodeBitmapBlock, fs.sb.BlocksCount),
			Suggestion: "Inode bitmap block number out of range",
		})
	}

	// Check free counts match block/inode counts
	if gd.FreeBlocksCount > fs.sb.BlocksPerGroup {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityWarning,
			Location:   fmt.Sprintf("group %d free_blocks", groupNum),
			Issue:      fmt.Sprintf("Free blocks count %d exceeds blocks per group %d", gd.FreeBlocksCount, fs.sb.BlocksPerGroup),
			Suggestion: "Block bitmap may be corrupted",
		})
	}

	if gd.FreeInodesCount > fs.sb.InodesPerGroup {
		reports = append(reports, CorruptionReport{
			Severity:   SeverityWarning,
			Location:   fmt.Sprintf("group %d free_inodes", groupNum),
			Issue:      fmt.Sprintf("Free inodes count %d exceeds inodes per group %d", gd.FreeInodesCount, fs.sb.InodesPerGroup),
			Suggestion: "Inode bitmap may be corrupted",
		})
	}

	return reports
}

// DetectCircularReferences attempts to find circular inode references (basic check).
// This is a limited check that looks for obvious cycles in parent inode links.
func (fs *FS) DetectCircularReferences(startInode uint32, maxDepth int) (bool, error) {
	visited := make(map[uint32]bool)

	return fs.hasCircularRef(startInode, visited, maxDepth)
}

// hasCircularRef recursively checks for circular references.
func (fs *FS) hasCircularRef(inodeNum uint32, visited map[uint32]bool, depth int) (bool, error) {
	if depth <= 0 {
		// Depth limit reached - could indicate a cycle
		return true, nil
	}

	if visited[inodeNum] {
		// We've seen this inode before - circular reference detected
		return true, nil
	}

	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return false, nil // Invalid inode number
	}

	visited[inodeNum] = true

	// For directories, check parent reference (..)
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return false, err // Can't validate further
	}

	if inode.IsDirectory {
		// Try to read ".." entry
		entries, err := fs.ListDir(inodeNum)
		if err != nil {
			// If we can't read directory, assume no circular ref
			return false, nil
		}

		// Find parent inode (..)
		for _, entry := range entries {
			if entry.Name == ".." {
				// Recursively check parent
				hasCycle, err := fs.hasCircularRef(entry.Inode, visited, depth-1)
				if err != nil || hasCycle {
					return hasCycle, err
				}
				break
			}
		}
	}

	delete(visited, inodeNum) // Backtrack for other paths
	return false, nil
}

// ScanForOrphanedInodes identifies inodes with link count > 0 but no directory
// references.
//
// Deprecated: this function has never returned results — it walks the inode
// table but never records a candidate, so it always returns nil. It is retained
// only for source compatibility. Use the deleted-inode enumeration API instead
// once available.
func (fs *FS) ScanForOrphanedInodes(maxInodesToCheck int) []uint32 {
	var orphans []uint32

	checkCount := 0
	for i := uint32(fs.sb.FirstInode); i <= fs.sb.InodesCount && checkCount < maxInodesToCheck; i++ {
		inode, err := fs.ReadInode(i)
		if err != nil {
			continue
		}

		// Skip deleted inodes
		if inode.LinksCount == 0 {
			continue
		}

		// Skip special inodes
		if i == 2 { // root
			continue
		}

		// Mark as potential orphan (full check would require directory tree traversal)
		checkCount++
	}

	return orphans
}

// ReportCorruptions returns a formatted string summarizing all detected corruptions.
func ReportCorruptions(reports []CorruptionReport) string {
	if len(reports) == 0 {
		return "No corruptions detected"
	}

	var output string
	var critCount, warnCount, infoCount int

	for _, r := range reports {
		switch r.Severity {
		case SeverityCritical:
			critCount++
		case SeverityWarning:
			warnCount++
		case SeverityInfo:
			infoCount++
		}
	}

	output = fmt.Sprintf("Corruption Report: %d critical, %d warnings, %d informational\n\n",
		critCount, warnCount, infoCount)

	for _, r := range reports {
		severity := "INFO"
		switch r.Severity {
		case SeverityCritical:
			severity = "CRITICAL"
		case SeverityWarning:
			severity = "WARNING"
		}

		output += fmt.Sprintf("[%s] %s\n", severity, r.Location)
		output += fmt.Sprintf("  Issue: %s\n", r.Issue)
		output += fmt.Sprintf("  Suggestion: %s\n\n", r.Suggestion)
	}

	return output
}
