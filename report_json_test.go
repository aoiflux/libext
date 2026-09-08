package libext

import (
	"encoding/json"
	"testing"
)

// Report and journal JSON shape.
//
// These are the tests that keep the encoding honest rather than merely present.
// The recurring hazard in a forensic report is a field that renders a value it
// does not have: a zero time.Time encodes as 0001-01-01T00:00:00Z, which reads
// as an answer rather than as an absence. omitzero is what prevents that, and
// omitempty - the reflex - does nothing at all for a struct field.

func findFile(t *testing.T, rep EXTReport, name string) EXTFile {
	t.Helper()
	for _, f := range rep.Files {
		if f.Filename == name {
			return f
		}
	}
	t.Fatalf("report has no entry for %q", name)
	return EXTFile{}
}

func TestReportShallowCarriesIdentity(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	rep, err := fs.Report("t")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	deep := findFile(t, rep, "/sub/deep.txt")
	if deep.InodeNumber != nestedDeep {
		t.Errorf("InodeNumber = %d, want %d", deep.InodeNumber, nestedDeep)
	}
	if deep.ParentInode != nestedSub {
		t.Errorf("ParentInode = %d, want %d", deep.ParentInode, nestedSub)
	}

	notes := findFile(t, rep, "/notes.txt")
	if notes.Generation != testGeneration {
		t.Errorf("Generation = %#x, want %#x", notes.Generation, testGeneration)
	}
	if notes.ParentInode != RootInode {
		t.Errorf("ParentInode = %d, want %d", notes.ParentInode, RootInode)
	}
	if notes.Times.Mtime.Unix() != 0x69e58838 {
		t.Errorf("Times.Mtime = %v, want unix %#x", notes.Times.Mtime, 0x69e58838)
	}
}

func TestReportDeepCarriesIdentity(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	rep, err := fs.ReportDeep("t")
	if err != nil {
		t.Fatalf("ReportDeep: %v", err)
	}

	notes := findFile(t, rep, "/notes.txt")
	if notes.InodeNumber != nestedNotes {
		t.Errorf("InodeNumber = %d, want %d", notes.InodeNumber, nestedNotes)
	}
	if notes.Generation != testGeneration {
		t.Errorf("Generation = %#x, want %#x", notes.Generation, testGeneration)
	}
	// The deep path gets the parent from the shared index, not from a walk.
	if notes.ParentInode != RootInode {
		t.Errorf("ParentInode = %d, want %d", notes.ParentInode, RootInode)
	}

	// An inode the tree no longer reaches is still reported, named by number,
	// and honestly says it has no known parent.
	gone := findFile(t, rep, "inode:41")
	if gone.InodeNumber != nestedGone {
		t.Errorf("InodeNumber = %d, want %d", gone.InodeNumber, nestedGone)
	}
	if gone.ParentInode != 0 {
		t.Errorf("ParentInode = %d for an unreachable inode, want 0", gone.ParentInode)
	}
	if !gone.IsDeleted {
		t.Error("the unlinked inode is not marked deleted")
	}
}

// TestReportJSONOmitsUnrecordedTimestamps is the omitzero test: it fails if
// anyone changes those tags back to omitempty, which would silently start
// emitting year-1 timestamps for times the inode never recorded.
func TestReportJSONOmitsUnrecordedTimestamps(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	rep, err := fs.Report("t")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	notes := findFile(t, rep, "/notes.txt")

	raw, err := json.Marshal(notes)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	times, ok := decoded["times"].(map[string]any)
	if !ok {
		t.Fatalf("times is %T, want an object: %s", decoded["times"], raw)
	}

	// notes.txt has atime, mtime and ctime; it is a live inode on a 128-byte
	// inode table, so it has neither a creation nor a deletion time.
	for _, key := range []string{"atime", "mtime", "ctime"} {
		if _, ok := times[key]; !ok {
			t.Errorf("recorded timestamp %q is missing from %s", key, raw)
		}
	}
	for _, key := range []string{"crtime", "dtime"} {
		if v, ok := times[key]; ok {
			t.Errorf("unrecorded timestamp %q was emitted as %v; a time the inode does "+
				"not carry must be absent, not rendered as a zero date", key, v)
		}
	}
}

// TestReportJSONKeySet guards the identity fields against acquiring an
// omitempty, which would drop inode_number 0 or generation 0 from a row and
// make the schema depend on the data.
func TestReportJSONKeySet(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	rep, err := fs.Report("t")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	// deep.txt has generation 0, which is exactly the value an omitempty would
	// swallow.
	deep := findFile(t, rep, "/sub/deep.txt")
	if deep.Generation != 0 {
		t.Fatalf("fixture no longer exercises the zero-generation case (got %#x)", deep.Generation)
	}

	raw, err := json.Marshal(deep)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{
		"filename", "inode_number", "generation", "parent_inode",
		"type", "is_fragmented", "is_deleted", "size", "times", "fragments",
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Errorf("key %q missing from %s", key, raw)
		}
	}
	if len(decoded) != len(want) {
		t.Errorf("EXTFile encoded %d keys, want %d: %s", len(decoded), len(want), raw)
	}
}

// ---------------------------------------------------------------------------
// journal
// ---------------------------------------------------------------------------

func TestJournalTransactionJSONKeys(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, []byte("payload"), 100), Options{})

	txs, err := fs.ListJournalTransactions()
	if err != nil {
		t.Fatalf("ListJournalTransactions: %v", err)
	}
	if len(txs) == 0 {
		t.Fatal("no transactions in the fixture")
	}

	raw, err := json.Marshal(txs[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{"sequence", "start_block", "type", "is_committed", "block_count", "tags"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("key %q missing from %s", key, raw)
		}
	}

	tags, ok := decoded["tags"].([]any)
	if !ok || len(tags) == 0 {
		t.Fatalf("tags is %T with no entries: %s", decoded["tags"], raw)
	}
	tag, ok := tags[0].(map[string]any)
	if !ok {
		t.Fatalf("tag is %T, want an object", tags[0])
	}
	for _, key := range []string{"fs_block", "journal_block", "escaped", "same_uuid", "last_tag"} {
		if _, ok := tag[key]; !ok {
			t.Errorf("tag key %q missing from %s", key, raw)
		}
	}
}

// TestJournalTransactionJSONOmitsUncommittedTimestamp is E5's real point. A
// transaction whose commit block was never written did not commit, and the
// absence of a commit time is the evidence for that. Encoding a zero time would
// fabricate the very event the absence disproves.
func TestJournalTransactionJSONOmitsUncommittedTimestamp(t *testing.T) {
	// The stock fixture's commit block is at journal block 3; overwrite it with
	// zeros so the descriptor's transaction is left uncommitted.
	img := buildJournalFixture(t, []byte("payload"), 100)
	writeTestBlock(img, testJournalFirstBlock+3, make([]byte, testBlockSize))

	fs := openFixture(t, img, Options{})

	txs, err := fs.ListJournalTransactions()
	if err != nil {
		t.Fatalf("ListJournalTransactions: %v", err)
	}
	if len(txs) == 0 {
		t.Fatal("no transactions in the fixture")
	}
	tx := txs[0]
	if tx.IsCommitted {
		t.Fatal("fixture still has a commit block; the uncommitted case is not being exercised")
	}
	if !tx.Timestamp.IsZero() {
		t.Fatalf("uncommitted transaction carries a timestamp: %v", tx.Timestamp)
	}

	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := decoded["timestamp"]; ok {
		t.Errorf("uncommitted transaction encoded timestamp %v; it must be absent", v)
	}
	if committed, _ := decoded["is_committed"].(bool); committed {
		t.Error("is_committed is true for a transaction with no commit block")
	}
}

func TestJournalTransactionJSONKeepsCommittedTimestamp(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, []byte("payload"), 100), Options{})

	txs, err := fs.ListJournalTransactions()
	if err != nil {
		t.Fatalf("ListJournalTransactions: %v", err)
	}
	if len(txs) == 0 || !txs[0].IsCommitted {
		t.Fatal("fixture does not contain a committed transaction")
	}

	raw, err := json.Marshal(txs[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := decoded["timestamp"]; !ok {
		t.Errorf("a committed transaction dropped its timestamp: %s", raw)
	}
}
