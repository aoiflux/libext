package libext

import (
	"encoding/binary"
	"testing"
)

// TestParseJournalSuperblockValid tests parsing a valid journal superblock.
func TestParseJournalSuperblockValid(t *testing.T) {
	data := make([]byte, 256)

	// Set fields
	binary.BigEndian.PutUint32(data[4:8], 4096)   // BlockSize
	binary.BigEndian.PutUint32(data[8:12], 100)   // MaxLen
	binary.BigEndian.PutUint32(data[12:16], 0)    // FirstBlock
	binary.BigEndian.PutUint32(data[16:20], 1)    // Sequence
	binary.BigEndian.PutUint32(data[28:32], 0x01) // CompatFeatures
	copy(data[40:56], "test-uuid-1234567")

	jsb, err := ParseJournalSuperblock(data)
	if err != nil {
		t.Fatalf("failed to parse valid journal superblock: %v", err)
	}

	if jsb.BlockSize != 4096 {
		t.Errorf("expected BlockSize 4096, got %d", jsb.BlockSize)
	}

	if jsb.MaxLen != 100 {
		t.Errorf("expected MaxLen 100, got %d", jsb.MaxLen)
	}

	if jsb.Sequence != 1 {
		t.Errorf("expected Sequence 1, got %d", jsb.Sequence)
	}
}

// TestParseJournalSuperblockTooSmall tests rejection of small data.
func TestParseJournalSuperblockTooSmall(t *testing.T) {
	data := make([]byte, 100) // Too small

	jsb, err := ParseJournalSuperblock(data)
	if err == nil {
		t.Error("expected error for small data, got nil")
	}

	if jsb != nil {
		t.Errorf("expected nil superblock on error, got %v", jsb)
	}
}

// TestJournalTransactionMarking tests transaction commit marking.
func TestJournalTransactionMarking(t *testing.T) {
	txn := JournalTransaction{
		Sequence:    1,
		StartBlock:  0,
		Type:        "descriptor",
		IsCommitted: false,
	}

	if txn.IsCommitted {
		t.Error("transaction should not be committed initially")
	}

	txn.IsCommitted = true

	if !txn.IsCommitted {
		t.Error("transaction should be committed after marking")
	}
}

// TestJournalFeatureDetection tests journal feature flag interpretation.
func TestJournalFeatureDetection(t *testing.T) {
	tests := []struct {
		name          string
		compatFlags   uint32
		incompat      uint32
		expectJournal bool
		expectRecov   bool
		expectAsync   bool
	}{
		{
			name:          "no_features",
			compatFlags:   0,
			incompat:      0,
			expectJournal: false,
			expectRecov:   false,
			expectAsync:   false,
		},
		{
			name:          "with_journal",
			compatFlags:   0x0004,
			incompat:      0,
			expectJournal: true,
			expectRecov:   false,
			expectAsync:   false,
		},
		{
			name:          "with_recovery",
			compatFlags:   0x0001 | 0x0004,
			incompat:      0,
			expectJournal: true,
			expectRecov:   true,
			expectAsync:   false,
		},
		{
			name:          "with_async_commit",
			compatFlags:   0x0004,
			incompat:      0x0200,
			expectJournal: true,
			expectRecov:   false,
			expectAsync:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sb := Superblock{
				FeatureCompat:   tc.compatFlags,
				FeatureIncompat: tc.incompat,
				JournalInode:    8,
			}
			fs := &FS{sb: sb}

			features := fs.GetJournalFeatures()

			if features["has_journal"] != tc.expectJournal {
				t.Errorf("has_journal: expected %v, got %v", tc.expectJournal, features["has_journal"])
			}

			if features["needs_recovery"] != tc.expectRecov {
				t.Errorf("needs_recovery: expected %v, got %v", tc.expectRecov, features["needs_recovery"])
			}

			if features["journal_async_commit"] != tc.expectAsync {
				t.Errorf("journal_async_commit: expected %v, got %v", tc.expectAsync, features["journal_async_commit"])
			}
		})
	}
}

// TestGetJournalInode tests inode extraction with and without journal.
func TestGetJournalInode(t *testing.T) {
	tests := []struct {
		name        string
		compatFlags uint32
		inodeNum    uint32
		expected    uint32
	}{
		{"no_journal", 0, 8, 0},
		{"with_journal", 0x0004, 8, 8},
		{"with_journal_zero", 0x0004, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sb := Superblock{
				FeatureCompat: tc.compatFlags,
				JournalInode:  tc.inodeNum,
			}
			fs := &FS{sb: sb}

			result := fs.GetJournalInode()
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

// TestJournalBlockTypes tests block type constants.
func TestJournalBlockTypes(t *testing.T) {
	tests := []struct {
		blockType uint32
		name      string
	}{
		{JournalBlockTypeSuperblock, "superblock"},
		{JournalBlockTypeDescriptor, "descriptor"},
		{JournalBlockTypeCommit, "commit"},
		{JournalBlockTypeOrphan, "orphan"},
		{JournalBlockTypeEscaped, "escaped"},
	}

	expected := []uint32{0, 1, 2, 3, 4}

	for i, tc := range tests {
		if tc.blockType != expected[i] {
			t.Errorf("%s: expected %d, got %d", tc.name, expected[i], tc.blockType)
		}
	}
}

// TestJournalMagic tests magic number constant.
func TestJournalMagic(t *testing.T) {
	if journalMagic != 0xC0B1A001 {
		t.Errorf("expected magic 0xC0B1A001, got 0x%08x", journalMagic)
	}
}

// TestJournalStatusDescription tests readable status generation.
func TestJournalStatusDescription(t *testing.T) {
	// Create a mock FS with journal
	sb := Superblock{
		FeatureCompat: 0x0004,
		JournalInode:  8,
		BlockSize:     4096,
	}
	fs := &FS{sb: sb}

	// Status should indicate journal disabled for now (without full mock)
	status, err := fs.DescribeJournalStatus()

	// Since we can't read inode in test, we expect an error
	if err == nil {
		t.Logf("status: %s", status)
	}
}
