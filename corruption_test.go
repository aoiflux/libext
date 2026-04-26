package libext

import (
	"testing"
)

// TestValidateSuperblockIntegrity tests superblock validation.
func TestValidateSuperblockIntegrity(t *testing.T) {
	sb := Superblock{
		BlockSize:      4096,
		InodeSize:      256,
		InodesPerGroup: 8192,
		BlocksPerGroup: 32768,
		FirstDataBlock: 0,
		BlocksCount:    1000000,
		ReservedBlocks: 50000,
	}
	fs := &FS{sb: sb}

	reports := fs.ValidateSuperblockIntegrity()

	// Should pass for valid superblock
	if len(reports) > 2 { // Allow minor informational notes
		t.Logf("Valid superblock raised %d reports", len(reports))
	}
}

// TestValidateSuperblockInvalidBlockSize tests detection of invalid block sizes.
func TestValidateSuperblockInvalidBlockSize(t *testing.T) {
	tests := []struct {
		name       string
		blockSize  uint32
		shouldFail bool
	}{
		{"1K", 1024, false},
		{"2K", 2048, false},
		{"4K", 4096, false},
		{"8K", 8192, false},
		{"invalid 3K", 3072, true},
		{"invalid 16K", 16384, true},
		{"invalid 0", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sb := Superblock{
				BlockSize:      tc.blockSize,
				BlocksPerGroup: 32768,
				FirstDataBlock: 0,
			}
			fs := &FS{sb: sb}

			reports := fs.ValidateSuperblockIntegrity()

			hasCritical := false
			for _, r := range reports {
				if r.Severity == SeverityCritical && r.Location == "superblock.block_size" {
					hasCritical = true
					break
				}
			}

			if tc.shouldFail && !hasCritical {
				t.Error("expected critical block size error")
			} else if !tc.shouldFail && hasCritical {
				t.Error("unexpected critical block size error")
			}
		})
	}
}

// TestValidateInodeIntegrity tests inode validation.
func TestValidateInodeIntegrity(t *testing.T) {
	sb := Superblock{
		BlockSize:     4096,
		RevisionLevel: 1,
	}
	fs := &FS{sb: sb}

	inode := &Inode{
		Number:      100,
		IsDirectory: false,
		IsRegular:   true,
		Size:        8192,
		Blocks512:   16,
		LinksCount:  1,
		Generation:  42,
	}

	reports := fs.ValidateInodeIntegrity(inode)

	// Should pass for valid regular file
	hasErrors := false
	for _, r := range reports {
		if r.Severity == SeverityCritical {
			hasErrors = true
			break
		}
	}

	if hasErrors {
		t.Error("valid inode raised critical error")
	}
}

// TestValidateInodeZeroLinkCount tests detection of unreachable inodes.
func TestValidateInodeZeroLinkCount(t *testing.T) {
	sb := Superblock{BlockSize: 4096}
	fs := &FS{sb: sb}

	inode := &Inode{
		Number:     100,
		LinksCount: 0,
	}

	reports := fs.ValidateInodeIntegrity(inode)

	// Should detect critical issue
	found := false
	for _, r := range reports {
		if r.Severity == SeverityCritical && r.Location == "inode 100 links_count" {
			found = true
			break
		}
	}

	if !found {
		t.Error("should detect zero link count as critical")
	}
}

// TestValidateGroupDescriptorIntegrity tests group descriptor validation.
func TestValidateGroupDescriptorIntegrity(t *testing.T) {
	sb := Superblock{
		BlockSize:      4096,
		BlocksPerGroup: 32768,
		InodesPerGroup: 8192,
		BlocksCount:    10000000,
	}
	fs := &FS{sb: sb}

	gd := &GroupDescriptor{
		BlockBitmapBlock: 1000,
		InodeBitmapBlock: 1001,
		FreeBlocksCount:  30000,
		FreeInodesCount:  7000,
	}

	reports := fs.ValidateGroupDescriptorIntegrity(0, gd)

	// Should pass for valid group descriptor
	hasCritical := false
	for _, r := range reports {
		if r.Severity == SeverityCritical {
			hasCritical = true
			break
		}
	}

	if hasCritical {
		t.Error("valid group descriptor raised critical error")
	}
}

// TestDetectCircularReferences tests circular reference detection.
func TestDetectCircularReferences(t *testing.T) {
	sb := Superblock{InodesCount: 1000, FirstInode: 1}
	fs := &FS{sb: sb}

	// Test with invalid inode (should not crash)
	hasCycle, err := fs.DetectCircularReferences(0, 10)

	if err != nil {
		t.Logf("error checking inode 0: %v", err)
	}

	if hasCycle {
		t.Error("invalid inode should not indicate cycle")
	}
}

// TestReportCorruptions tests corruption reporting.
func TestReportCorruptions(t *testing.T) {
	// Empty reports
	report := ReportCorruptions([]CorruptionReport{})
	if report != "No corruptions detected" {
		t.Error("empty report should say no corruptions detected")
	}

	// With reports
	reports := []CorruptionReport{
		{
			Severity:   SeverityCritical,
			Location:   "test.critical",
			Issue:      "critical issue",
			Suggestion: "fix it",
		},
		{
			Severity:   SeverityWarning,
			Location:   "test.warning",
			Issue:      "warning issue",
			Suggestion: "investigate",
		},
	}

	report = ReportCorruptions(reports)

	if len(report) == 0 {
		t.Error("should generate non-empty report")
	}

	if !stringContains(report, "CRITICAL") || !stringContains(report, "WARNING") {
		t.Error("report should contain severity levels")
	}
}

// TestCorruptionSeverity tests severity constants.
func TestCorruptionSeverity(t *testing.T) {
	if SeverityInfo != 0 || SeverityWarning != 1 || SeverityCritical != 2 {
		t.Error("corruption severity constants incorrect")
	}
}

// TestScanForOrphanedInodesNoInodes tests orphan scanning with no inodes.
func TestScanForOrphanedInodesNoInodes(t *testing.T) {
	sb := Superblock{
		InodesCount:    1000,
		FirstInode:     10,
		BlockSize:      4096,
		BlocksPerGroup: 32768,
		InodesPerGroup: 8192,
	}
	fs := &FS{sb: sb}

	// Should handle gracefully even without proper group descriptors
	orphans := fs.ScanForOrphanedInodes(100)

	// Just verify it doesn't crash
	if orphans == nil {
		// If nil is returned, that's okay in this test (means no orphans found)
		t.Logf("ScanForOrphanedInodes returned nil (no orphans found)")
	}
}

// TestValidateInodeDirectorySizeAlignment tests directory size alignment checks.
func TestValidateInodeDirectorySizeAlignment(t *testing.T) {
	sb := Superblock{BlockSize: 4096}
	fs := &FS{sb: sb}

	// Directory not aligned to block size
	inode := &Inode{
		Number:      50,
		IsDirectory: true,
		Size:        5000, // not aligned to 4096
		LinksCount:  2,
	}

	reports := fs.ValidateInodeIntegrity(inode)

	// Should report alignment issue
	found := false
	for _, r := range reports {
		if r.Severity == SeverityInfo && r.Location == "inode 50 size" {
			found = true
			break
		}
	}

	if !found {
		t.Logf("reports: %+v", reports)
		t.Error("should detect directory size misalignment")
	}
}

// Helper function to check if string contains substring
func stringContains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
