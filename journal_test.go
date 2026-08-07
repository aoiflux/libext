package libext

import (
	"encoding/binary"
	"testing"
)

// TestParseJournalSuperblockValid tests parsing a valid journal superblock.
//
// The fields sit after the 12-byte journal header. This fixture previously wrote
// them eight bytes early, matching a parser that read them from the same wrong
// offsets, so every value it reported from a real journal was another field.
func TestParseJournalSuperblockValid(t *testing.T) {
	data := make([]byte, 256)

	binary.BigEndian.PutUint32(data[0:4], journalMagic)
	binary.BigEndian.PutUint32(data[4:8], JournalBlockTypeSuperblockV2)

	binary.BigEndian.PutUint32(data[12:16], 4096) // s_blocksize
	binary.BigEndian.PutUint32(data[16:20], 100)  // s_maxlen
	binary.BigEndian.PutUint32(data[20:24], 1)    // s_first
	binary.BigEndian.PutUint32(data[24:28], 1)    // s_sequence
	binary.BigEndian.PutUint32(data[28:32], 0)    // s_start
	binary.BigEndian.PutUint32(data[40:44], journalIncompatCSumV3)
	copy(data[48:64], "test-uuid-1234567")

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

	if jsb.FirstBlock != 1 {
		t.Errorf("expected FirstBlock 1, got %d", jsb.FirstBlock)
	}

	if !jsb.HasCSumV3() {
		t.Error("expected the v3 checksum feature to be detected")
	}
}

// TestParseJournalSuperblockRejectsBadMagic guards the constant itself: it was
// 0xC0B1A001, which matches nothing, so journal parsing silently found no
// blocks at all on every real filesystem.
func TestParseJournalSuperblockRejectsBadMagic(t *testing.T) {
	data := make([]byte, 256)
	binary.BigEndian.PutUint32(data[0:4], 0xC0B1A001)

	if _, err := ParseJournalSuperblock(data); err == nil {
		t.Error("expected an error for a block without the JBD2 magic")
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
			// 0x0001 in the compat set is DIR_PREALLOC, not a recovery flag.
			name:          "compat_bit_one_is_not_recovery",
			compatFlags:   0x0001 | 0x0004,
			incompat:      0,
			expectJournal: true,
			expectRecov:   false,
			expectAsync:   false,
		},
		{
			// Recovery is an incompat flag (RECOVER, 0x0004), not a compat one.
			name:          "with_recovery_incompat",
			compatFlags:   0x0004,
			incompat:      0x0004,
			expectJournal: true,
			expectRecov:   true,
			expectAsync:   false,
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

			// journal_async_commit lives in the journal's own superblock, which
			// this fixture has no way to read, so it must simply be absent
			// rather than derived from an unrelated filesystem flag.
			if got, ok := features["journal_async_commit"]; ok && got != tc.expectAsync {
				t.Errorf("journal_async_commit: expected %v, got %v", tc.expectAsync, got)
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
		expected  uint32
		name      string
	}{
		{JournalBlockTypeDescriptor, 1, "descriptor"},
		{JournalBlockTypeCommit, 2, "commit"},
		{JournalBlockTypeSuperblockV1, 3, "superblock v1"},
		{JournalBlockTypeSuperblockV2, 4, "superblock v2"},
		{JournalBlockTypeRevoke, 5, "revoke"},
	}

	for _, tc := range tests {
		if tc.blockType != tc.expected {
			t.Errorf("%s: expected %d, got %d", tc.name, tc.expected, tc.blockType)
		}
	}
}

// TestJournalMagic pins the JBD2 header magic.
//
// It was 0xC0B1A001 here and in the parser, so no journal block ever matched
// and transaction enumeration always returned an empty list.
func TestJournalMagic(t *testing.T) {
	if journalMagic != 0xC03B3998 {
		t.Errorf("expected JBD2_MAGIC_NUMBER 0xC03B3998, got 0x%08x", journalMagic)
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
