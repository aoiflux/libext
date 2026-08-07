package libext

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// sbConfig describes the superblock fields a test needs to vary. Everything else
// is filled with a small, valid ext2 geometry: 1 KiB blocks, one block group.
type sbConfig struct {
	blocksCount    uint64
	firstDataBlock uint32
	blocksPerGroup uint32
	inodesCount    uint32
	inodesPerGroup uint32
	logBlockSize   uint32
	inodeSize      uint16
	revision       uint32
	compat         uint32
	incompat       uint32
	roCompat       uint32
}

func defaultSBConfig() sbConfig {
	return sbConfig{
		blocksCount:    1024,
		firstDataBlock: 1,
		blocksPerGroup: 8192,
		inodesCount:    64,
		inodesPerGroup: 64,
		logBlockSize:   0, // 1 KiB blocks, so the descriptor table sits at 2048
		inodeSize:      128,
		revision:       1,
		incompat:       featureIncompatFileType,
	}
}

// buildTestImage renders cfg into a 1 MiB image with a zeroed group descriptor
// table, which is enough for Open to succeed.
func buildTestImage(t testing.TB, cfg sbConfig) []byte {
	t.Helper()

	img := make([]byte, 1<<20)
	sb := img[superblockOffset : superblockOffset+superblockSize]

	binary.LittleEndian.PutUint32(sb[0x00:], cfg.inodesCount)
	binary.LittleEndian.PutUint32(sb[0x04:], uint32(cfg.blocksCount))
	binary.LittleEndian.PutUint32(sb[0x14:], cfg.firstDataBlock)
	binary.LittleEndian.PutUint32(sb[0x18:], cfg.logBlockSize)
	binary.LittleEndian.PutUint32(sb[0x20:], cfg.blocksPerGroup)
	binary.LittleEndian.PutUint32(sb[0x28:], cfg.inodesPerGroup)
	binary.LittleEndian.PutUint16(sb[0x38:], extMagic)
	binary.LittleEndian.PutUint16(sb[0x3A:], 1) // clean
	binary.LittleEndian.PutUint32(sb[0x4C:], cfg.revision)
	binary.LittleEndian.PutUint32(sb[0x54:], 11)
	binary.LittleEndian.PutUint16(sb[0x58:], cfg.inodeSize)
	binary.LittleEndian.PutUint32(sb[0x5C:], cfg.compat)
	binary.LittleEndian.PutUint32(sb[0x60:], cfg.incompat)
	binary.LittleEndian.PutUint32(sb[0x64:], cfg.roCompat)

	// 64-bit block count high word, so BlocksCount survives the >32-bit cases.
	binary.LittleEndian.PutUint32(sb[0x150:], uint32(cfg.blocksCount>>32))

	// One group descriptor at block 2, pointing the inode table at block 5.
	gd := img[testGroupDescOffset:]
	binary.LittleEndian.PutUint32(gd[0x00:], 3) // block bitmap
	binary.LittleEndian.PutUint32(gd[0x04:], 4) // inode bitmap
	binary.LittleEndian.PutUint32(gd[0x08:], testInodeTableBlock)

	return img
}

const (
	testGroupDescOffset = 2048
	testInodeTableBlock = 5
	testBlockSize       = 1024
	testInodeSize       = 128
)

// writeTestInode places a raw inode into the fixture's inode table.
func writeTestInode(img []byte, num uint32, mode uint16, size uint32, flags uint32, blockRaw []byte) {
	off := testInodeTableBlock*testBlockSize + int(num-1)*testInodeSize
	raw := img[off : off+testInodeSize]

	binary.LittleEndian.PutUint16(raw[0x00:], mode)
	binary.LittleEndian.PutUint32(raw[0x04:], size)
	binary.LittleEndian.PutUint16(raw[0x1A:], 1) // links count
	binary.LittleEndian.PutUint32(raw[0x20:], flags)
	copy(raw[0x28:0x28+60], blockRaw)
}

func newFixtureReader(img []byte) io.ReaderAt {
	return bytes.NewReader(img)
}

func hasWarning(warnings []Warning, code WarningCode) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// B3: bounds checks must apply to a SectionReader-backed volume
// ---------------------------------------------------------------------------

func TestOpenProbesSectionReaderSize(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// io.SectionReader exposes Size but not Stat. Probing only for Stat left
	// imageSize at 0, which disabled every bounds check.
	sr := io.NewSectionReader(bytes.NewReader(img), 0, int64(len(img)))

	fs, err := Open(sr)
	if err != nil {
		t.Fatalf("Open(SectionReader): %v", err)
	}
	if got := fs.Options().ImageSize; got != uint64(len(img)) {
		t.Errorf("probed image size = %d, want %d", got, len(img))
	}
	if err := fs.readAt(uint64(len(img)), make([]byte, 1)); err == nil {
		t.Error("read past end of image was accepted; bounds checks are not active")
	}
}

func TestOpenWithSizeKeepsUnboundedBehaviour(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// OpenWithSize documents 0 as "skip bounds checks"; that contract predates
	// this work and must not change under callers.
	fs, err := OpenWithSize(bytes.NewReader(img), 0)
	if err != nil {
		t.Fatalf("OpenWithSize: %v", err)
	}
	if fs.Options().ImageSize != 0 {
		t.Errorf("OpenWithSize(r, 0) probed the reader; image size = %d, want 0", fs.Options().ImageSize)
	}
}

// ---------------------------------------------------------------------------
// B1: geometry that underflows the group count must not allocate
// ---------------------------------------------------------------------------

func TestOpenRejectsGeometryUnderflow(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*sbConfig)
	}{
		{
			// FirstDataBlock > BlocksCount underflowed BlocksCount-FirstDataBlock,
			// producing a group count near 2^32 and a ~190 GB allocation.
			name: "first data block beyond block count",
			mod:  func(c *sbConfig) { c.firstDataBlock = 2000; c.blocksCount = 1024 },
		},
		{
			name: "first data block equals block count",
			mod:  func(c *sbConfig) { c.firstDataBlock = 1024; c.blocksCount = 1024 },
		},
		{
			name: "zero block count",
			mod:  func(c *sbConfig) { c.blocksCount = 0 },
		},
		{
			name: "group count over the limit",
			mod:  func(c *sbConfig) { c.blocksCount = 1 << 40; c.blocksPerGroup = 1 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultSBConfig()
			tt.mod(&cfg)
			img := buildTestImage(t, cfg)

			_, err := Open(bytes.NewReader(img))
			if err == nil {
				t.Fatal("Open accepted invalid geometry")
			}
			if !errors.Is(err, ErrInvalidSuperblock) {
				t.Errorf("error = %v, want ErrInvalidSuperblock", err)
			}
		})
	}
}

func TestOpenRejectsUnreadableGroupDescriptorTable(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// Truncate so the superblock parses but the descriptor table does not fit.
	truncated := img[:2048]

	_, err := Open(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("Open accepted an image too small for its descriptor table")
	}
	if !errors.Is(err, ErrInvalidSuperblock) {
		t.Errorf("error = %v, want ErrInvalidSuperblock", err)
	}
}

func TestOpenTruncatedImageWarns(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.blocksCount = 4096 // claims 4 MiB against a 1 MiB reader
	img := buildTestImage(t, cfg)

	fs, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !hasWarning(fs.Warnings(), WarnTruncatedImage) {
		t.Errorf("no truncation warning recorded; got %v", fs.Warnings())
	}
}

// ---------------------------------------------------------------------------
// B4/B5/B7: feature gate
// ---------------------------------------------------------------------------

func lookupFeature(t testing.TB, flagType, name string) Feature {
	t.Helper()
	for _, f := range AllFeatures {
		if f.FlagType == flagType && f.Name == name {
			return f
		}
	}
	t.Fatalf("feature %s:%s is not described", flagType, name)
	return Feature{}
}

func TestFeatureTableFlagValues(t *testing.T) {
	// 0x0400 was labelled LARGEDIR; in ext4 that bit is EA_INODE and LARGEDIR is
	// 0x4000. Anything gating on the table gated on the wrong feature.
	tests := []struct {
		flagType string
		name     string
		value    uint32
	}{
		{"incompat", "EA_INODE", 0x0400},
		{"incompat", "DIRDATA", 0x1000},
		{"incompat", "CSUM_SEED", 0x2000},
		{"incompat", "LARGEDIR", 0x4000},
		{"incompat", "INLINE_DATA", 0x8000},
		{"incompat", "ENCRYPT", 0x10000},
		{"incompat", "CASEFOLD", 0x20000},
		{"compat", "FAST_COMMIT", 0x0400},
		{"compat", "STABLE_INODES", 0x0800},
		{"compat", "ORPHAN_FILE", 0x1000},
		{"ro_compat", "BIGALLOC", 0x0200},
		{"ro_compat", "VERITY", 0x8000},
		{"ro_compat", "ORPHAN_PRESENT", 0x10000},
	}

	for _, tt := range tests {
		t.Run(tt.flagType+":"+tt.name, func(t *testing.T) {
			if got := lookupFeature(t, tt.flagType, tt.name).FlagValue; got != tt.value {
				t.Errorf("FlagValue = 0x%x, want 0x%x", got, tt.value)
			}
		})
	}
}

func TestFeatureTableHasNoDuplicateBits(t *testing.T) {
	seen := make(map[string]string)
	for _, f := range AllFeatures {
		key := fmt.Sprintf("%s:%#x", f.FlagType, f.FlagValue)
		if prev, ok := seen[key]; ok {
			t.Errorf("%s bit 0x%x is claimed by both %s and %s", f.FlagType, f.FlagValue, prev, f.Name)
		}
		seen[key] = f.Name
	}
}

func TestOpenFeatureGate(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*sbConfig)
		want string // substring of the rejection reason
	}{
		{
			// An unrecognised incompat bit means the layout may differ in ways
			// the parser cannot detect.
			name: "unknown incompat bit",
			mod:  func(c *sbConfig) { c.incompat |= 0x40000 },
			want: "unrecognised feature bits",
		},
		{
			// Extents address clusters under BIGALLOC, so every physical offset
			// would be scaled wrongly. It is ro_compat, so the old incompat-only
			// gate let it through.
			name: "bigalloc",
			mod:  func(c *sbConfig) { c.roCompat |= 0x0200 },
			want: "BIGALLOC",
		},
		{
			name: "meta_bg",
			mod:  func(c *sbConfig) { c.incompat |= 0x0010 },
			want: "META_BG",
		},
		{
			name: "journal_dev",
			mod:  func(c *sbConfig) { c.incompat |= 0x0008 },
			want: "JOURNAL_DEV",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultSBConfig()
			tt.mod(&cfg)
			img := buildTestImage(t, cfg)

			_, err := Open(bytes.NewReader(img))
			if err == nil {
				t.Fatal("Open accepted an image it cannot parse correctly")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}

			// Permissive turns the same condition into a warning.
			fs, err := OpenWithOptions(bytes.NewReader(img), Options{Permissive: true})
			if err != nil {
				t.Fatalf("permissive Open: %v", err)
			}
			if len(fs.Warnings()) == 0 {
				t.Error("permissive Open recorded no warning")
			}
		})
	}
}

func TestOpenUnknownBitWarnsPerFeatureSet(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.roCompat |= 0x80000 // undescribed, and ro_compat so it does not block
	img := buildTestImage(t, cfg)

	fs, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !hasWarning(fs.Warnings(), WarnUnknownFeature) {
		t.Errorf("no unknown-feature warning recorded; got %v", fs.Warnings())
	}
	if got := fs.UnknownFeatureBits("ro_compat"); got != 0x80000 {
		t.Errorf("UnknownFeatureBits = 0x%x, want 0x80000", got)
	}
}

// ---------------------------------------------------------------------------
// B2: extent tree traversal must terminate
// ---------------------------------------------------------------------------

func extentHeader(entries, max, depth uint16) []byte {
	h := make([]byte, extentHeaderSize)
	binary.LittleEndian.PutUint16(h[0:], extentHeaderMagic)
	binary.LittleEndian.PutUint16(h[2:], entries)
	binary.LittleEndian.PutUint16(h[4:], max)
	binary.LittleEndian.PutUint16(h[6:], depth)
	return h
}

func extentLeafEntry(logical uint32, length uint16, phys uint64) []byte {
	e := make([]byte, extentEntrySize)
	binary.LittleEndian.PutUint32(e[0:], logical)
	binary.LittleEndian.PutUint16(e[4:], length)
	binary.LittleEndian.PutUint16(e[6:], uint16(phys>>32))
	binary.LittleEndian.PutUint32(e[8:], uint32(phys))
	return e
}

func extentIndexEntry(logical uint32, leaf uint64) []byte {
	e := make([]byte, extentEntrySize)
	binary.LittleEndian.PutUint32(e[0:], logical)
	binary.LittleEndian.PutUint32(e[4:], uint32(leaf))
	binary.LittleEndian.PutUint16(e[8:], uint16(leaf>>32))
	return e
}

// newExtentFS builds an FS whose reader holds the given blocks, so extent index
// entries can be pointed at crafted nodes.
func newExtentFS(blocks map[uint64][]byte) *FS {
	const blockSize = 1024
	const blocksCount = 64

	img := make([]byte, blockSize*blocksCount)
	for num, data := range blocks {
		copy(img[num*blockSize:], data)
	}

	return &FS{
		r:         bytes.NewReader(img),
		imageSize: uint64(len(img)),
		sb: Superblock{
			BlockSize:   blockSize,
			BlocksCount: blocksCount,
		},
	}
}

// inodeRoot packs a node into the 60 bytes an inode reserves for its block map.
func inodeRoot(parts ...[]byte) []byte {
	root := make([]byte, 60)
	off := 0
	for _, p := range parts {
		off += copy(root[off:], p)
	}
	return root
}

func TestExtentTreeRejectsSelfReferencingIndex(t *testing.T) {
	// Block 5 is an index node pointing at itself. Before depth was checked
	// against the parent, this recursed until the stack was exhausted — a crash
	// no recover can catch.
	selfRef := append(extentHeader(1, 84, 1), extentIndexEntry(0, 5)...)
	fs := newExtentFS(map[uint64][]byte{5: selfRef})

	root := inodeRoot(extentHeader(1, 4, 2), extentIndexEntry(0, 5))

	_, err := fs.parseExtentTree(root)
	if err == nil {
		t.Fatal("self-referencing extent tree was accepted")
	}
	if !errors.Is(err, ErrUnsupportedLayout) {
		t.Errorf("error = %v, want ErrUnsupportedLayout", err)
	}
}

func TestExtentTreeRejectsRepeatedIndexBlock(t *testing.T) {
	// Two index entries pointing at the same block. Legitimate trees never share
	// a child, so a repeat is either corruption or a cycle being set up.
	leaf := append(extentHeader(1, 84, 1), extentIndexEntry(0, 9)...)
	fs := newExtentFS(map[uint64][]byte{
		5: leaf,
		9: append(extentHeader(1, 84, 0), extentLeafEntry(0, 1, 20)...),
	})

	root := inodeRoot(extentHeader(2, 4, 2), extentIndexEntry(0, 5), extentIndexEntry(1, 5))

	_, err := fs.parseExtentTree(root)
	if err == nil {
		t.Fatal("repeated extent index block was accepted")
	}
	if !strings.Contains(err.Error(), "revisited") {
		t.Errorf("error = %v, want it to report a revisited block", err)
	}
}

func TestExtentTreeRejectsMalformedNodes(t *testing.T) {
	tests := []struct {
		name   string
		blocks map[uint64][]byte
		root   []byte
	}{
		{
			// The in-inode root holds 60 bytes: a 12-byte header and at most 4
			// entries. The old bound of 340 let a node read far past its buffer.
			name: "entry count exceeds node capacity",
			root: inodeRoot(extentHeader(340, 340, 0)),
		},
		{
			name: "entry count exceeds header max",
			root: inodeRoot(extentHeader(4, 2, 0), extentLeafEntry(0, 1, 20)),
		},
		{
			name: "depth beyond format limit",
			root: inodeRoot(extentHeader(1, 4, 9), extentIndexEntry(0, 5)),
		},
		{
			// Only the root used to be magic-checked; children were trusted.
			name:   "child node missing magic",
			blocks: map[uint64][]byte{5: make([]byte, 1024)},
			root:   inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 5)),
		},
		{
			name: "index block beyond filesystem",
			root: inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 9999)),
		},
		{
			name: "index block zero",
			root: inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 0)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newExtentFS(tt.blocks)
			if _, err := fs.parseExtentTree(tt.root); err == nil {
				t.Fatal("malformed extent node was accepted")
			}
		})
	}
}

func TestExtentTreeParsesValidTree(t *testing.T) {
	// Positive control: the hardening must not reject well-formed trees.
	fs := newExtentFS(map[uint64][]byte{
		5: append(extentHeader(2, 84, 0),
			append(extentLeafEntry(0, 4, 20), extentLeafEntry(4, 2, 40)...)...),
	})

	root := inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 5))

	recs, err := fs.parseExtentTree(root)
	if err != nil {
		t.Fatalf("parseExtentTree: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].physical != 20 || recs[0].length != 4 {
		t.Errorf("record 0 = %+v, want physical 20 length 4", recs[0])
	}
	if recs[1].logicalStart != 4 || recs[1].physical != 40 {
		t.Errorf("record 1 = %+v, want logical 4 physical 40", recs[1])
	}
}

func TestExtentTreeRespectsMaxExtents(t *testing.T) {
	fs := newExtentFS(map[uint64][]byte{
		5: append(extentHeader(3, 84, 0),
			append(extentLeafEntry(0, 1, 20),
				append(extentLeafEntry(1, 1, 21), extentLeafEntry(2, 1, 22)...)...)...),
	})
	fs.opts.MaxExtents = 2

	root := inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 5))

	if _, err := fs.parseExtentTree(root); err == nil {
		t.Fatal("extent count limit was not enforced")
	}
}

func TestExtentInodeDoesNotFallBackToClassicMap(t *testing.T) {
	// An inode flagged as extent-mapped whose tree is unparseable must report the
	// failure. Falling through to the classic block map reinterprets the extent
	// header as block pointers and returns fabricated offsets.
	fs := newExtentFS(nil)

	inode := Inode{Number: 12, Size: 4096, IsRegular: true, HasExtents: true}
	copy(inode.BlockRaw[:], inodeRoot(extentHeader(340, 340, 0)))

	if _, err := fs.InodeExtents(inode, ExtentOptions{}); err == nil {
		t.Fatal("unparseable extent tree silently fell back to the classic block map")
	}
}

// ---------------------------------------------------------------------------
// B6: a damaged record must not discard the whole directory
// ---------------------------------------------------------------------------

func dirent(inode uint32, recLen uint16, fileType uint8, name string) []byte {
	e := make([]byte, recLen)
	binary.LittleEndian.PutUint32(e[0:], inode)
	binary.LittleEndian.PutUint16(e[4:], recLen)
	e[6] = uint8(len(name))
	e[7] = fileType
	copy(e[8:], name)
	return e
}

// damagedDirData lays out two good records, eight bytes of damage, then a third
// good record aligned so that resynchronisation can reach it.
func damagedDirData() []byte {
	var data []byte
	data = append(data, dirent(1, 12, 1, "a")...)
	data = append(data, dirent(2, 12, 1, "b")...)
	data = append(data, bytes.Repeat([]byte{0xFF}, 8)...)
	data = append(data, dirent(3, 12, 1, "c")...)
	return data
}

func TestParseDirEntriesReturnsPartialResults(t *testing.T) {
	fs := &FS{sb: Superblock{FeatureIncompat: featureIncompatFileType}}

	entries, err := fs.parseDirEntries(damagedDirData())
	if err == nil {
		t.Fatal("damaged directory data parsed without error")
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want the 2 parsed before the fault: %+v", len(entries), entries)
	}
	if entries[0].Name != "a" || entries[1].Name != "b" {
		t.Errorf("entries = %q, %q; want \"a\", \"b\"", entries[0].Name, entries[1].Name)
	}
}

func TestParseDirEntriesResynchronisesWhenPermissive(t *testing.T) {
	fs := &FS{
		sb:   Superblock{FeatureIncompat: featureIncompatFileType},
		opts: Options{Permissive: true},
	}

	entries, err := fs.parseDirEntries(damagedDirData())
	if err != nil {
		t.Fatalf("permissive parse returned an error: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if !containsName(names, "c") {
		t.Errorf("entries after the damage were not recovered; got %q", names)
	}
	if !hasWarning(fs.Warnings(), WarnDegradedRead) {
		t.Error("no degraded-read warning recorded for the damaged record")
	}
}

func TestParseDirEntriesStopsAtZeroRecordLength(t *testing.T) {
	// A zero record length terminates the stream; it is a truncated tail, not
	// damage, and must not be reported as an error.
	fs := &FS{sb: Superblock{FeatureIncompat: featureIncompatFileType}}

	data := append(dirent(1, 12, 1, "a"), make([]byte, 16)...)

	entries, err := fs.parseDirEntries(data)
	if err != nil {
		t.Fatalf("zero-length record reported as damage: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a" {
		t.Errorf("entries = %+v, want a single entry \"a\"", entries)
	}
}

func TestParseDirEntriesRejectsUnalignedRecord(t *testing.T) {
	fs := &FS{sb: Superblock{FeatureIncompat: featureIncompatFileType}}

	data := append(dirent(1, 12, 1, "a"), dirent(2, 13, 1, "b")...)

	if _, err := fs.parseDirEntries(data); err == nil {
		t.Fatal("unaligned record length was accepted")
	}
}

// ---------------------------------------------------------------------------
// C8 and warning plumbing
// ---------------------------------------------------------------------------

func TestOpenWarnsOnChecksumSeedFeature(t *testing.T) {
	// With CSUM_SEED set, checksums are keyed on s_checksum_seed rather than the
	// volume UUID, so verifying against the UUID reports false mismatches.
	cfg := defaultSBConfig()
	cfg.incompat |= featureIncompatCSumSeed
	img := buildTestImage(t, cfg)

	fs, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !hasWarning(fs.Warnings(), WarnChecksumMismatch) {
		t.Errorf("no checksum-seed warning recorded; got %v", fs.Warnings())
	}
}

func TestWarningsAreCapped(t *testing.T) {
	fs := &FS{}
	for i := 0; i < maxWarnings*3; i++ {
		fs.warn(WarnDegradedRead, "", "noise")
	}

	got := fs.Warnings()
	if len(got) != maxWarnings {
		t.Fatalf("recorded %d warnings, want the cap of %d", len(got), maxWarnings)
	}
	if !strings.Contains(got[len(got)-1].Detail, "suppressed") {
		t.Errorf("last warning = %q, want it to report suppression", got[len(got)-1].Detail)
	}
}

// ---------------------------------------------------------------------------
// regression: one unreadable block map must not discard a report
// ---------------------------------------------------------------------------

func TestReportDeepToleratesUnreadableBlockMap(t *testing.T) {
	// Hardening the extent parser made inodeBlockNumber return errors where it
	// previously fell back to the classic map. A deep scan walks unallocated
	// inode table entries full of garbage, so aborting on the first one would
	// discard the whole report.
	img := buildTestImage(t, defaultSBConfig())

	garbage := make([]byte, 60)
	for i := range garbage {
		garbage[i] = 0xAB
	}
	// Regular file, non-zero size, extent-mapped flag set, unparseable tree.
	writeTestInode(img, 12, 0x81A4, 4096, inodeFlagExtents, garbage)

	fs, err := Open(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	report, err := fs.ReportDeep("fixture")
	if err != nil {
		t.Fatalf("ReportDeep aborted on an unreadable block map: %v", err)
	}

	var found bool
	for _, f := range report.Files {
		if strings.Contains(f.Filename, "inode:12") {
			found = true
			if len(f.Fragments) != 0 {
				t.Errorf("inode 12 reported %d fragments, want none", len(f.Fragments))
			}
		}
	}
	if !found {
		t.Error("inode 12 was dropped from the report entirely")
	}
	if !hasWarning(fs.Warnings(), WarnDegradedRead) {
		t.Error("no degraded-read warning recorded for the unreadable block map")
	}
}

// ---------------------------------------------------------------------------
// concurrency: FS read methods are documented as safe for concurrent use
// ---------------------------------------------------------------------------

func TestConcurrentReadsAreRaceFree(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	writeTestInode(img, 12, 0x81A4, 0, 0, nil)

	fs, err := OpenWithOptions(bytes.NewReader(img), Options{Permissive: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = fs.ReadInode(12)
				_, _ = fs.ListDir(2)
				_ = fs.Warnings()
			}
		}()
	}
	wg.Wait()
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
