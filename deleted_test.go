package libext

import (
	"encoding/binary"
	"testing"
)

// ---------------------------------------------------------------------------
// fixture helpers
// ---------------------------------------------------------------------------

const (
	testBlockBitmapBlock = 3
	testInodeBitmapBlock = 4
	testDirBlock         = 200
)

// setBitmapBit sets bit n of the bitmap stored in a fixture block.
func setBitmapBit(img []byte, block uint64, n uint64) {
	img[block*testBlockSize+n/8] |= 1 << (n % 8)
}

// setGroupFlags patches the group descriptor's flags word.
func setGroupFlags(img []byte, flags uint16) {
	binary.LittleEndian.PutUint16(img[testGroupDescOffset+0x12:], flags)
}

// setItableUnused patches bg_itable_unused.
func setItableUnused(img []byte, n uint16) {
	binary.LittleEndian.PutUint16(img[testGroupDescOffset+0x1C:], n)
}

// setLastOrphan patches s_last_orphan in the superblock.
func setLastOrphan(img []byte, inode uint32) {
	binary.LittleEndian.PutUint32(img[superblockOffset+0xE8:], inode)
}

// writeDeletedInode places an unlinked inode: a deletion time and no links.
func writeDeletedInode(img []byte, num uint32, size uint32, blockRaw []byte, dtime uint32) {
	raw := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], size)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 0)
		binary.LittleEndian.PutUint32(raw[inodeOffDtime:], dtime)
		copy(raw[inodeOffBlockRaw:], blockRaw)
	})
	copy(img[testInodeTableBlock*testBlockSize+int(num-1)*testInodeSize:], raw)
}

// buildDeletedFixture lays out a root directory whose slack holds the record of
// a deleted file, plus the deleted inode itself.
func buildDeletedFixture(t testing.TB) []byte {
	t.Helper()

	img := buildTestImage(t, defaultSBConfig())

	// Root directory: ".", "..", a live file, and a record whose rec_len has
	// been extended to swallow a deleted neighbour, exactly as unlink does.
	var dir []byte
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, ".")...)
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, "..")...)

	live := dirent(40, 40, 1, "keep.txt") // 8+8=16 padded to 16; rec_len 40
	// The swallowed record starts where the live entry's name ends.
	copy(live[16:], dirent(41, 20, 1, "gone.txt"))
	dir = append(dir, live...)

	dir = append(dir, dirent(42, uint16(testBlockSize-64), 1, "other.txt")...)
	writeTestBlock(img, testDirBlock, dir)

	writeTestInode(img, 2, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{testDirBlock}, 0, 0, 0))

	// keep.txt: live.
	keep := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 100)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
	})
	copy(img[testInodeTableBlock*testBlockSize+39*testInodeSize:], keep)

	// gone.txt: unlinked, block map intact, blocks 100-101.
	writeDeletedInode(img, 41, 2*testBlockSize,
		classicRoot([]uint32{100, 101}, 0, 0, 0), 0x69e58900)

	// other.txt: live.
	other := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
	})
	copy(img[testInodeTableBlock*testBlockSize+41*testInodeSize:], other)

	// Inode bitmap: root, keep.txt and other.txt allocated; gone.txt released.
	for _, n := range []uint32{2, 40, 42} {
		setBitmapBit(img, testInodeBitmapBlock, uint64(n-1))
	}
	// Only the inode table region is marked allocated; blocks 100-101 are free,
	// so the deleted file's content has not been overwritten.
	for b := uint64(0); b < 20; b++ {
		setBitmapBit(img, testBlockBitmapBlock, b)
	}

	// Everything past inode 42 has never been used.
	setItableUnused(img, uint16(defaultSBConfig().inodesPerGroup-42))

	return img
}

// ---------------------------------------------------------------------------
// bitmaps
// ---------------------------------------------------------------------------

func TestInodeAndBlockAllocation(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	for _, tc := range []struct {
		inode uint32
		want  bool
	}{
		{2, true},   // root
		{40, true},  // keep.txt
		{41, false}, // gone.txt, released on unlink
		{42, true},  // other.txt
	} {
		got, err := fs.InodeAllocated(tc.inode)
		if err != nil {
			t.Fatalf("InodeAllocated(%d): %v", tc.inode, err)
		}
		if got != tc.want {
			t.Errorf("InodeAllocated(%d) = %v, want %v", tc.inode, got, tc.want)
		}
	}

	for _, tc := range []struct {
		block uint64
		want  bool
	}{
		{5, true},    // inode table
		{100, false}, // the deleted file's data, not yet reused
	} {
		got, err := fs.BlockAllocated(tc.block)
		if err != nil {
			t.Fatalf("BlockAllocated(%d): %v", tc.block, err)
		}
		if got != tc.want {
			t.Errorf("BlockAllocated(%d) = %v, want %v", tc.block, got, tc.want)
		}
	}
}

func TestBitmapsOfUninitGroupReadAsEmpty(t *testing.T) {
	// An uninitialised group's bitmap block holds whatever preceded the
	// filesystem. Reporting those bytes as allocation state would invent
	// allocations, so the group reads as entirely free.
	img := buildDeletedFixture(t)
	for i := 0; i < testBlockSize; i++ {
		img[testInodeBitmapBlock*testBlockSize+i] = 0xFF
	}
	setGroupFlags(img, GroupInodeUninit)

	fs := openFixture(t, img, Options{})

	bitmap, err := fs.InodeBitmap(0)
	if err != nil {
		t.Fatalf("InodeBitmap: %v", err)
	}
	for i, b := range bitmap {
		if b != 0 {
			t.Fatalf("byte %d of an uninitialised group's bitmap = 0x%02x, want 0", i, b)
		}
	}

	allocated, err := fs.InodeAllocated(2)
	if err != nil {
		t.Fatalf("InodeAllocated: %v", err)
	}
	if allocated {
		t.Error("inode reported allocated from an uninitialised group")
	}
}

func TestBlockAllocatedRejectsOutOfRange(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	if _, err := fs.BlockAllocated(1 << 40); err == nil {
		t.Error("BlockAllocated accepted a block beyond the filesystem")
	}
	if _, err := fs.InodeAllocated(0); err == nil {
		t.Error("InodeAllocated accepted inode 0")
	}
}

// ---------------------------------------------------------------------------
// directory slack
// ---------------------------------------------------------------------------

func TestScanDirSlackRecoversDeletedName(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	slack, err := fs.ScanDirSlack(RootInode)
	if err != nil {
		t.Fatalf("ScanDirSlack: %v", err)
	}

	var found *DirSlackEntry
	for i := range slack {
		if slack[i].Name == "gone.txt" {
			found = &slack[i]
		}
	}
	if found == nil {
		t.Fatalf("gone.txt was not recovered from slack; got %+v", slack)
	}
	if found.Inode != 41 {
		t.Errorf("recovered inode = %d, want 41", found.Inode)
	}
	if found.ParentInode != RootInode {
		t.Errorf("ParentInode = %d, want %d", found.ParentInode, RootInode)
	}
	if found.ShadowsLive {
		t.Error("a genuinely deleted record was flagged as shadowing a live entry")
	}
}

func TestScanDirSlackFlagsShadowsOfLiveEntries(t *testing.T) {
	// Rewriting a directory leaves stale copies of records that are still live.
	// They are evidence of nothing, and reporting them as deletions is the main
	// source of false positives in slack recovery.
	img := buildTestImage(t, defaultSBConfig())

	var dir []byte
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, ".")...)
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, "..")...)

	live := dirent(40, 40, 1, "keep.txt")
	copy(live[16:], dirent(40, 20, 1, "keep.txt")) // stale duplicate of itself
	dir = append(dir, live...)
	dir = append(dir, dirent(42, uint16(testBlockSize-64), 1, "other.txt")...)

	writeTestBlock(img, testDirBlock, dir)
	writeTestInode(img, 2, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{testDirBlock}, 0, 0, 0))

	fs := openFixture(t, img, Options{})

	slack, err := fs.ScanDirSlack(RootInode)
	if err != nil {
		t.Fatalf("ScanDirSlack: %v", err)
	}
	if len(slack) == 0 {
		t.Fatal("the stale duplicate was not found at all")
	}
	for _, s := range slack {
		if s.Name == "keep.txt" && !s.ShadowsLive {
			t.Error("a stale copy of a live entry was not flagged as ShadowsLive")
		}
	}

	// And it must not surface as a deletion.
	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	for _, e := range entries {
		if e.Name == "keep.txt" {
			t.Errorf("a stale duplicate of a live entry was reported as deleted: %+v", e)
		}
	}
}

func TestScanDirSlackRejectsNonDirectory(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	if _, err := fs.ScanDirSlack(40); err == nil {
		t.Error("ScanDirSlack accepted a regular file")
	}
}

func TestPlausibleFileName(t *testing.T) {
	// Slack is mostly file data. Without this filter the scan reports binary
	// noise as recovered filenames.
	for _, bad := range []string{"", ".", "..", "a/b", "a\x00b", "a\x01b", "\x7f"} {
		if plausibleFileName(bad) {
			t.Errorf("plausibleFileName(%q) = true", bad)
		}
	}
	for _, good := range []string{"gone.txt", "a", "with space", "ünïcode"} {
		if !plausibleFileName(good) {
			t.Errorf("plausibleFileName(%q) = false", good)
		}
	}
}

// ---------------------------------------------------------------------------
// deleted enumeration
// ---------------------------------------------------------------------------

func TestDeletedEntriesJoinsNameToInode(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}

	var gone *DeletedEntry
	for i := range entries {
		if entries[i].Inode == 41 {
			gone = &entries[i]
		}
	}
	if gone == nil {
		t.Fatalf("the deleted inode was not reported; got %+v", entries)
	}

	// The inode table supplies the metadata and slack supplies the name; a
	// single entry must carry both rather than the deletion being reported twice.
	if gone.Name != "gone.txt" {
		t.Errorf("Name = %q, want %q", gone.Name, "gone.txt")
	}
	if gone.Path != "/gone.txt" {
		t.Errorf("Path = %q, want /gone.txt", gone.Path)
	}
	if gone.Source != DeletedSourceInodeTable {
		t.Errorf("Source = %v, want inode_table", gone.Source)
	}
	if gone.Times.Dtime.IsZero() {
		t.Error("deletion time was not carried through")
	}
	if gone.Allocated {
		t.Error("Allocated = true for an inode released on unlink")
	}

	count := 0
	for _, e := range entries {
		if e.Inode == 41 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("inode 41 reported %d times, want once", count)
	}
}

func TestDeletedEntriesJudgesRecoverability(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	for _, e := range entries {
		if e.Inode != 41 {
			continue
		}
		// The block map survived and blocks 100-101 are still free.
		if e.Recoverable != RecoveryLikely {
			t.Errorf("Recoverable = %v, want likely", e.Recoverable)
		}
		if len(e.Extents) == 0 {
			t.Error("the surviving block map was not reported")
		}
	}
}

func TestDeletedEntriesMarksReallocatedBlocksPartial(t *testing.T) {
	img := buildDeletedFixture(t)
	// Hand one of the deleted file's blocks to something else.
	setBitmapBit(img, testBlockBitmapBlock, 100-1)

	fs := openFixture(t, img, Options{})

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	for _, e := range entries {
		if e.Inode != 41 {
			continue
		}
		if e.Recoverable != RecoveryPartial {
			t.Errorf("Recoverable = %v, want partial once a block is reallocated", e.Recoverable)
		}
	}
}

func TestDeletedEntriesReportsNoneWithoutBlockMap(t *testing.T) {
	img := buildDeletedFixture(t)
	// ext4 zeroes the extent tree on unlink; emulate the map being gone.
	writeDeletedInode(img, 41, 2*testBlockSize, make([]byte, 60), 0x69e58900)

	fs := openFixture(t, img, Options{})

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	for _, e := range entries {
		if e.Inode != 41 {
			continue
		}
		if e.Recoverable != RecoveryNone {
			t.Errorf("Recoverable = %v, want none when no block map survives", e.Recoverable)
		}
		// The name still survives even though the content cannot be located.
		if e.Name != "gone.txt" {
			t.Errorf("Name = %q; the name outlives the block map", e.Name)
		}
	}
}

func TestDeletedScanSkipsUninitialisedGroups(t *testing.T) {
	// An uninitialised inode table holds pre-existing disk contents. Scanning it
	// yields phantom "deleted files" that were never files on this filesystem.
	img := buildDeletedFixture(t)
	for i := 0; i < 8*testBlockSize; i++ {
		img[testInodeTableBlock*testBlockSize+i] = 0xCC
	}
	setGroupFlags(img, GroupInodeUninit)

	fs := openFixture(t, img, Options{})

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("uninitialised group produced %d phantom entries", len(entries))
	}

	// Opting in reaches them.
	entries, err = fs.DeletedEntriesWithOptions(DeletedScanOptions{IncludeUninit: true, MaxResults: 5})
	if err != nil {
		t.Fatalf("DeletedEntriesWithOptions: %v", err)
	}
	if len(entries) == 0 {
		t.Error("IncludeUninit did not reach the uninitialised group")
	}
}

func TestDeletedScanHonoursItableUnused(t *testing.T) {
	// bg_itable_unused marks the tail of the table as never written.
	img := buildDeletedFixture(t)
	setItableUnused(img, uint16(defaultSBConfig().inodesPerGroup-20))

	fs := openFixture(t, img, Options{})

	err := fs.ScanDeleted(DeletedScanOptions{SkipDirSlack: true, SkipOrphanList: true},
		func(e DeletedEntry) error {
			if e.Inode > 20 {
				t.Errorf("inode %d is past bg_itable_unused and should not be scanned", e.Inode)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("ScanDeleted: %v", err)
	}
}

func TestScanDeletedStopsAtMaxResults(t *testing.T) {
	img := buildDeletedFixture(t)
	for n := uint32(20); n < 30; n++ {
		writeDeletedInode(img, n, 1024, classicRoot([]uint32{100}, 0, 0, 0), 0x69e58900)
	}

	fs := openFixture(t, img, Options{})

	entries, err := fs.DeletedEntriesWithOptions(DeletedScanOptions{MaxResults: 3})
	if err != nil {
		t.Fatalf("DeletedEntriesWithOptions: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want the 3 requested", len(entries))
	}
}

func TestScanDeletedPropagatesCallbackError(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	want := errScanStop // any sentinel distinct from the internal one
	err := fs.ScanDeleted(DeletedScanOptions{}, func(DeletedEntry) error {
		return want
	})
	if err != nil {
		// errScanStop is swallowed by design; use a different error to be sure.
		t.Logf("sentinel swallowed as designed: %v", err)
	}

	custom := errNotForScanning
	err = fs.ScanDeleted(DeletedScanOptions{}, func(DeletedEntry) error {
		return custom
	})
	if err != custom {
		t.Errorf("ScanDeleted error = %v, want the callback's error", err)
	}
}

var errNotForScanning = errTestSentinel{}

type errTestSentinel struct{}

func (errTestSentinel) Error() string { return "stop scanning" }

func TestScanDeletedRejectsNilCallback(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})
	if err := fs.ScanDeleted(DeletedScanOptions{}, nil); err == nil {
		t.Error("ScanDeleted accepted a nil callback")
	}
}

// ---------------------------------------------------------------------------
// orphans
// ---------------------------------------------------------------------------

func TestLegacyOrphanChain(t *testing.T) {
	// While an inode is on the orphan list, i_dtime holds the next inode number
	// rather than a timestamp. Walking it as a chain is the only way to read it.
	img := buildDeletedFixture(t)
	writeDeletedInode(img, 20, 1024, nil, 21) // -> 21
	writeDeletedInode(img, 21, 1024, nil, 22) // -> 22
	writeDeletedInode(img, 22, 1024, nil, 0)  // end of chain
	setLastOrphan(img, 20)

	fs := openFixture(t, img, Options{})

	orphans, err := fs.OrphanInodes()
	if err != nil {
		t.Fatalf("OrphanInodes: %v", err)
	}
	want := []uint32{20, 21, 22}
	if len(orphans) != len(want) {
		t.Fatalf("orphans = %v, want %v", orphans, want)
	}
	for i := range want {
		if orphans[i] != want[i] {
			t.Fatalf("orphans = %v, want %v", orphans, want)
		}
	}

	entries, err := fs.DeletedEntries()
	if err != nil {
		t.Fatalf("DeletedEntries: %v", err)
	}
	for _, e := range entries {
		if e.Inode != 20 {
			continue
		}
		if e.Source != DeletedSourceOrphanList {
			t.Errorf("Source = %v, want orphan_list", e.Source)
		}
		// The chain pointer must not be presented as a deletion time.
		if !e.Times.Dtime.IsZero() {
			t.Errorf("Dtime = %v; on the orphan list that field is a chain pointer",
				e.Times.Dtime)
		}
	}
}

func TestLegacyOrphanChainSurvivesCycle(t *testing.T) {
	img := buildDeletedFixture(t)
	writeDeletedInode(img, 20, 1024, nil, 21)
	writeDeletedInode(img, 21, 1024, nil, 20) // points back
	setLastOrphan(img, 20)

	fs := openFixture(t, img, Options{})

	orphans, err := fs.OrphanInodes()
	if err != nil {
		t.Fatalf("OrphanInodes: %v", err)
	}
	if len(orphans) != 2 {
		t.Errorf("orphans = %v, want the cycle broken after 2", orphans)
	}
	if !hasWarning(fs.Warnings(), WarnDegradedRead) {
		t.Error("no warning recorded for a broken orphan chain")
	}
}

func TestOrphanFileParsing(t *testing.T) {
	// On kernels with orphan_file the legacy chain is empty and the real list
	// lives in a dedicated inode, which is why reading only the chain reports
	// nothing on a modern filesystem.
	block := make([]byte, testBlockSize)
	binary.LittleEndian.PutUint32(block[0:], 31)
	binary.LittleEndian.PutUint32(block[4:], 0) // empty slot
	binary.LittleEndian.PutUint32(block[8:], 32)
	binary.LittleEndian.PutUint32(block[len(block)-8:], orphanFileMagic)

	got, ok := parseOrphanBlock(block)
	if !ok {
		t.Fatal("a block carrying the orphan magic was rejected")
	}
	if len(got) != 2 || got[0] != 31 || got[1] != 32 {
		t.Errorf("parseOrphanBlock = %v, want [31 32]", got)
	}

	// A block without the magic is not part of the list.
	block[len(block)-8] ^= 0xFF
	if _, ok := parseOrphanBlock(block); ok {
		t.Error("a block without the orphan magic was accepted")
	}
}

func TestOrphanFileInodeGatedOnFeature(t *testing.T) {
	img := buildDeletedFixture(t)
	binary.LittleEndian.PutUint32(img[superblockOffset+0x280:], 12)

	fs := openFixture(t, img, Options{})
	if got := fs.OrphanFileInode(); got != 0 {
		t.Errorf("OrphanFileInode = %d without the ORPHAN_FILE feature, want 0", got)
	}

	// With the feature set, the stored inode is reported.
	binary.LittleEndian.PutUint32(img[superblockOffset+0x5C:], featureCompatOrphanFile)
	fs = openFixture(t, img, Options{})
	if got := fs.OrphanFileInode(); got != 12 {
		t.Errorf("OrphanFileInode = %d, want 12", got)
	}
}
