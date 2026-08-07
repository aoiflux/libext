package libext

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Journal fixtures.
//
// The journal is big-endian and its blocks live inside an ordinary inode, so a
// fixture has to build both: an inode whose block map points at the journal
// blocks, and the JBD2 structures inside them.

const (
	testJournalInode      = 8
	testJournalFirstBlock = 300
)

// jbd2Header writes a journal block header.
func jbd2Header(block []byte, blockType, sequence uint32) {
	binary.BigEndian.PutUint32(block[0:], journalMagic)
	binary.BigEndian.PutUint32(block[4:], blockType)
	binary.BigEndian.PutUint32(block[8:], sequence)
}

// jbd2Superblock renders the journal superblock.
func jbd2Superblock(maxLen, sequence, incompat uint32) []byte {
	b := make([]byte, testBlockSize)
	jbd2Header(b, JournalBlockTypeSuperblockV2, 0)
	binary.BigEndian.PutUint32(b[12:], testBlockSize) // s_blocksize
	binary.BigEndian.PutUint32(b[16:], maxLen)        // s_maxlen
	binary.BigEndian.PutUint32(b[20:], 1)             // s_first
	binary.BigEndian.PutUint32(b[24:], sequence)      // s_sequence
	binary.BigEndian.PutUint32(b[28:], 0)             // s_start
	binary.BigEndian.PutUint32(b[40:], incompat)      // s_feature_incompat
	return b
}

// jbd2Descriptor renders a descriptor block naming the given filesystem blocks.
// Tags use the 8-byte layout with SAME_UUID set, so no UUID follows them.
func jbd2Descriptor(sequence uint32, fsBlocks []uint64) []byte {
	b := make([]byte, testBlockSize)
	jbd2Header(b, JournalBlockTypeDescriptor, sequence)

	off := journalHeaderSize
	for i, fsBlock := range fsBlocks {
		flags := uint32(JournalTagFlagSameBuf)
		if i == len(fsBlocks)-1 {
			flags |= JournalTagFlagLastTag
		}
		binary.BigEndian.PutUint32(b[off:], uint32(fsBlock))
		binary.BigEndian.PutUint16(b[off+4:], 0) // t_checksum
		binary.BigEndian.PutUint16(b[off+6:], uint16(flags))
		off += 8
	}
	return b
}

// jbd2Commit renders a commit block carrying a timestamp.
func jbd2Commit(sequence uint32, sec uint64, nsec uint32) []byte {
	b := make([]byte, testBlockSize)
	jbd2Header(b, JournalBlockTypeCommit, sequence)
	binary.BigEndian.PutUint64(b[48:], sec)
	binary.BigEndian.PutUint32(b[56:], nsec)
	return b
}

// buildJournalFixture assembles an image whose journal holds one transaction.
//
// Layout: block 300 journal superblock, 301 descriptor, 302 the journalled copy
// of a filesystem block, 303 commit.
func buildJournalFixture(t testing.TB, journalled []byte, fsBlock uint64) []byte {
	t.Helper()

	cfg := defaultSBConfig()
	cfg.compat = featureCompatHasJournal
	img := buildTestImage(t, cfg)

	binary.LittleEndian.PutUint32(img[superblockOffset+0xE0:], testJournalInode)

	writeTestBlock(img, testJournalFirstBlock+0, jbd2Superblock(4, 2, 0))
	writeTestBlock(img, testJournalFirstBlock+1, jbd2Descriptor(2, []uint64{fsBlock}))

	copyBlock := make([]byte, testBlockSize)
	copy(copyBlock, journalled)
	writeTestBlock(img, testJournalFirstBlock+2, copyBlock)

	writeTestBlock(img, testJournalFirstBlock+3, jbd2Commit(2, 0x69e58838, 490096625))

	// The journal inode maps those four blocks.
	writeTestInode(img, testJournalInode, inodeTypeRegular|0o600, 4*testBlockSize, 0,
		classicRoot([]uint32{
			testJournalFirstBlock, testJournalFirstBlock + 1,
			testJournalFirstBlock + 2, testJournalFirstBlock + 3,
		}, 0, 0, 0))

	return img
}

// ---------------------------------------------------------------------------

func TestJournalSuperblockFromImage(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, nil, 100), Options{})

	jsb, err := fs.JournalSuperblock()
	if err != nil {
		t.Fatalf("JournalSuperblock: %v", err)
	}
	if jsb.BlockSize != testBlockSize {
		t.Errorf("BlockSize = %d, want %d", jsb.BlockSize, testBlockSize)
	}
	if jsb.MaxLen != 4 || jsb.FirstBlock != 1 || jsb.Sequence != 2 {
		t.Errorf("jsb = %+v, want maxlen 4, first 1, sequence 2", jsb)
	}
}

func TestListJournalTransactionsReadsCommitTime(t *testing.T) {
	// The commit time comes from the commit block. It was previously stamped
	// with the wall clock at parse time, which fabricated a forensic artifact
	// that looked authoritative.
	fs := openFixture(t, buildJournalFixture(t, nil, 100), Options{})

	txns, err := fs.ListJournalTransactions()
	if err != nil {
		t.Fatalf("ListJournalTransactions: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("got %d transactions, want 1: %+v", len(txns), txns)
	}

	tx := txns[0]
	if tx.Sequence != 2 || tx.Type != "descriptor" {
		t.Errorf("transaction = %+v, want sequence 2 descriptor", tx)
	}
	if !tx.IsCommitted {
		t.Error("transaction not marked committed despite a commit block")
	}
	if tx.Timestamp.Unix() != 0x69e58838 {
		t.Errorf("Timestamp = %v (unix %d), want unix %d",
			tx.Timestamp, tx.Timestamp.Unix(), 0x69e58838)
	}
	if tx.Timestamp.Nanosecond() != 490096625 {
		t.Errorf("Timestamp nanoseconds = %d, want 490096625", tx.Timestamp.Nanosecond())
	}
}

func TestJournalTagsNameFilesystemBlocks(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, nil, 4242), Options{})

	txns, err := fs.ListJournalTransactions()
	if err != nil {
		t.Fatalf("ListJournalTransactions: %v", err)
	}
	if len(txns) != 1 || len(txns[0].Tags) != 1 {
		t.Fatalf("expected one transaction with one tag, got %+v", txns)
	}

	tag := txns[0].Tags[0]
	if tag.FSBlock != 4242 {
		t.Errorf("FSBlock = %d, want 4242", tag.FSBlock)
	}
	// The copy sits in the journal block immediately after the descriptor.
	if tag.JournalBlock != 2 {
		t.Errorf("JournalBlock = %d, want 2", tag.JournalBlock)
	}
	if !tag.LastTag {
		t.Error("the only tag should be flagged as last")
	}
}

func TestJournalTagSizeFollowsFeatures(t *testing.T) {
	// Three tag layouts exist and the journal's feature flags select between
	// them; guessing yields plausible but wrong block numbers.
	tests := []struct {
		name     string
		incompat uint32
		wantSize int
	}{
		{"base", 0, 8},
		{"64bit", journalIncompat64Bit, 12},
		{"csum v3", journalIncompatCSumV3, 16},
		{"csum v3 wins over 64bit", journalIncompatCSumV3 | journalIncompat64Bit, 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsb := &JournalSuperblock{IncompatFeatures: tt.incompat}

			desc := make([]byte, testBlockSize)
			jbd2Header(desc, JournalBlockTypeDescriptor, 1)
			// One tag, marked last, at the start of the tag array.
			binary.BigEndian.PutUint32(desc[journalHeaderSize:], 777)
			if jsb.HasCSumV3() {
				binary.BigEndian.PutUint32(desc[journalHeaderSize+4:],
					JournalTagFlagSameBuf|JournalTagFlagLastTag)
			} else {
				binary.BigEndian.PutUint16(desc[journalHeaderSize+6:],
					JournalTagFlagSameBuf|JournalTagFlagLastTag)
			}

			tags := parseJournalTags(desc, jsb, 1)
			if len(tags) != 1 {
				t.Fatalf("got %d tags, want 1", len(tags))
			}
			if tags[0].FSBlock != 777 {
				t.Errorf("FSBlock = %d, want 777", tags[0].FSBlock)
			}
		})
	}
}

func TestJournalBlockCopiesReturnsPriorContents(t *testing.T) {
	content := bytes.Repeat([]byte{0xAB}, 64)
	fs := openFixture(t, buildJournalFixture(t, content, 100), Options{})

	copies, err := fs.JournalBlockCopies(100)
	if err != nil {
		t.Fatalf("JournalBlockCopies: %v", err)
	}
	if len(copies) != 1 {
		t.Fatalf("got %d copies, want 1", len(copies))
	}
	if !bytes.Equal(copies[0][:64], content) {
		t.Errorf("journalled copy does not match what was written")
	}

	// A block the journal never carried has no copies.
	other, err := fs.JournalBlockCopies(999)
	if err != nil {
		t.Fatalf("JournalBlockCopies: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("got %d copies for an unjournalled block, want 0", len(other))
	}
}

func TestJournalInodeVersionsRecoversPriorState(t *testing.T) {
	// The inode table block is journalled with an earlier version of an inode:
	// larger, still linked, with an intact block map. This is what survives an
	// unlink that zeroed the live copy.
	priorBlock := make([]byte, testBlockSize)
	prior := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 9999)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
		copy(raw[inodeOffBlockRaw:], classicRoot([]uint32{100, 101}, 0, 0, 0))
	})
	// Inode 3 sits at the fourth slot of the first inode table block.
	copy(priorBlock[2*testInodeSize:], prior)

	img := buildJournalFixture(t, priorBlock, testInodeTableBlock)

	// The live inode 3 has been emptied, as unlink leaves it.
	writeTestInode(img, 3, inodeTypeRegular|0o644, 0, 0, nil)

	fs := openFixture(t, img, Options{})

	versions, err := fs.JournalInodeVersions(3)
	if err != nil {
		t.Fatalf("JournalInodeVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(versions))
	}
	if versions[0].Size != 9999 {
		t.Errorf("recovered size = %d, want 9999", versions[0].Size)
	}
	if versions[0].LinksCount != 1 {
		t.Errorf("recovered links = %d, want 1", versions[0].LinksCount)
	}

	// The live inode really is empty, so the journal is the only source.
	live, err := fs.ReadInode(3)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if live.Size != 0 {
		t.Errorf("live size = %d, want 0", live.Size)
	}
}

func TestJournalInodeVersionsRejectsBadInode(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, nil, 100), Options{})

	if _, err := fs.JournalInodeVersions(0); err == nil {
		t.Error("JournalInodeVersions accepted inode 0")
	}
}

// ---------------------------------------------------------------------------
// fast commit
// ---------------------------------------------------------------------------

// fcRecord renders one tag/length fast-commit record.
func fcRecord(tag FastCommitTag, body []byte) []byte {
	out := make([]byte, fastCommitTLSize+len(body))
	binary.BigEndian.PutUint16(out[0:], uint16(tag))
	binary.BigEndian.PutUint16(out[2:], uint16(len(body)))
	copy(out[fastCommitTLSize:], body)
	return out
}

// fcDentry renders the body of a creat/link/unlink record.
func fcDentry(parent, inode uint32, name string) []byte {
	body := make([]byte, 8+len(name))
	binary.BigEndian.PutUint32(body[0:], parent)
	binary.BigEndian.PutUint32(body[4:], inode)
	copy(body[8:], name)
	return body
}

func TestParseFastCommitUnlink(t *testing.T) {
	// An unlink record carries the parent inode and the filename, and is often
	// the only record of a very recent deletion.
	block := make([]byte, testBlockSize)
	off := 0
	for _, rec := range [][]byte{
		fcRecord(FastCommitTagCreat, fcDentry(2, 40, "created.txt")),
		fcRecord(FastCommitTagUnlink, fcDentry(2, 41, "deleted.txt")),
		fcRecord(FastCommitTagTail, make([]byte, 8)),
	} {
		copy(block[off:], rec)
		off += len(rec)
	}

	ops := parseFastCommitBlock(block, 7)
	if len(ops) != 2 {
		t.Fatalf("got %d operations, want 2: %+v", len(ops), ops)
	}

	if ops[0].Tag != FastCommitTagCreat || ops[0].Name != "created.txt" || ops[0].Inode != 40 {
		t.Errorf("op 0 = %+v", ops[0])
	}

	unlink := ops[1]
	if unlink.Tag != FastCommitTagUnlink {
		t.Errorf("op 1 tag = %v, want unlink", unlink.Tag)
	}
	if unlink.Name != "deleted.txt" {
		t.Errorf("unlinked name = %q, want deleted.txt", unlink.Name)
	}
	if unlink.Inode != 41 || unlink.Parent != 2 {
		t.Errorf("unlink inode/parent = %d/%d, want 41/2", unlink.Inode, unlink.Parent)
	}
	if unlink.Block != 7 {
		t.Errorf("Block = %d, want 7", unlink.Block)
	}
}

func TestParseFastCommitStopsAtTail(t *testing.T) {
	block := make([]byte, testBlockSize)
	off := 0
	for _, rec := range [][]byte{
		fcRecord(FastCommitTagUnlink, fcDentry(2, 41, "first.txt")),
		fcRecord(FastCommitTagTail, make([]byte, 8)),
		fcRecord(FastCommitTagUnlink, fcDentry(2, 42, "after-tail.txt")),
	} {
		copy(block[off:], rec)
		off += len(rec)
	}

	ops := parseFastCommitBlock(block, 0)
	for _, op := range ops {
		if op.Name == "after-tail.txt" {
			t.Error("parsing continued past the tail record")
		}
	}
}

func TestFastCommitOpsAbsentWithoutFeature(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, nil, 100), Options{})

	ops, err := fs.FastCommitOps()
	if err != nil {
		t.Fatalf("FastCommitOps: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("got %d operations from a journal without fast commit", len(ops))
	}
}

func TestFastCommitTagString(t *testing.T) {
	if got := FastCommitTagUnlink.String(); got != "unlink" {
		t.Errorf("FastCommitTagUnlink.String() = %q, want unlink", got)
	}
	if got := FastCommitTag(999).String(); got != "tag999" {
		t.Errorf("unknown tag = %q, want tag999", got)
	}
}
