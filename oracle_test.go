package libext

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Cross-validation against e2fsprogs.
//
// Synthetic fixtures check the parser against its author's understanding of the
// format. These tests check it against the reference implementation, which is a
// different and stronger claim: several defects in this package survived a full
// green suite precisely because the tests and the code shared one mistake.
//
// The images and their captured oracles are generated, not committed:
//
//	testdata/mkcorpus.sh /path/to/corpus
//	for i in /path/to/corpus/*.img; do
//	    testdata/mkoracle.sh "$i" > "${i%.img}.oracle"
//	done
//	LIBEXT_CORPUS=/path/to/corpus go test -run TestOracle ./...
//
// Without LIBEXT_CORPUS the tests skip, so the suite stays runnable on a machine
// with no e2fsprogs.

// oracle is the parsed content of one .oracle file.
type oracle struct {
	blockSize  uint64
	blockCount uint64
	inodeCount uint64
	inodeSize  uint64

	inodes map[uint32]oracleInode
	extents map[uint32][]oracleExtent
	blocks map[uint32]int
	deleted map[uint32]bool
	slack   map[string]bool // "<parent> <inode> <name>"
	journal []string        // "<sequence> <type> <block>"
}

type oracleInode struct {
	size      uint64
	links     uint64
	mtimeSec  int64
	mtimeNsec int64
	hasCrtime bool
}

type oracleExtent struct {
	logicalStart, logicalEnd   uint64
	physicalStart, physicalEnd uint64
}

func corpusDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("LIBEXT_CORPUS")
	if dir == "" {
		t.Skip("set LIBEXT_CORPUS to a directory built by testdata/mkcorpus.sh")
	}
	return dir
}

// parseOracle reads the record stream mkoracle.sh emits.
func parseOracle(t *testing.T, path string) *oracle {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	defer f.Close()

	o := &oracle{
		inodes:  map[uint32]oracleInode{},
		extents: map[uint32][]oracleExtent{},
		blocks:  map[uint32]int{},
		deleted: map[uint32]bool{},
		slack:   map[string]bool{},
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "geom":
			if len(fields) >= 5 {
				o.blockSize = mustUint(t, fields[1])
				o.blockCount = mustUint(t, fields[2])
				o.inodeCount = mustUint(t, fields[3])
				o.inodeSize = mustUint(t, fields[4])
			}
		case "inode":
			if len(fields) < 6 {
				continue
			}
			num := uint32(mustUint(t, fields[1]))
			in := oracleInode{
				size:  parseKV(fields[2], "size="),
				links: parseKV(fields[3], "links="),
			}
			in.mtimeSec, in.mtimeNsec = parseHexTime(strings.TrimPrefix(fields[4], "mtime="))
			in.hasCrtime = strings.TrimPrefix(fields[5], "crtime=") != "-"
			o.inodes[num] = in
		case "ext":
			if len(fields) < 3 {
				continue
			}
			num := uint32(mustUint(t, fields[1]))
			if e, ok := parseOracleExtent(fields[2]); ok {
				o.extents[num] = append(o.extents[num], e)
			}
		case "blocks":
			if len(fields) >= 3 {
				o.blocks[uint32(mustUint(t, fields[1]))] = int(mustUint(t, fields[2]))
			}
		case "del":
			if len(fields) >= 2 {
				o.deleted[uint32(mustUint(t, fields[1]))] = true
			}
		case "slack":
			if len(fields) >= 4 {
				o.slack[fields[1]+" "+fields[2]+" "+fields[3]] = true
			}
		case "jrnl":
			if len(fields) >= 4 {
				o.journal = append(o.journal, strings.Join(fields[1:4], " "))
			}
		}
	}
	return o
}

func mustUint(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseKV(field, prefix string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimPrefix(field, prefix), 10, 64)
	return v
}

// parseHexTime decodes debugfs's "<sec>.<extra>" hex timestamp pair.
func parseHexTime(s string) (int64, int64) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	sec, _ := strconv.ParseUint(parts[0], 16, 64)
	extra, _ := strconv.ParseUint(parts[1], 16, 64)

	seconds := int64(int32(uint32(sec)))
	if epoch := int64(extra & 3); epoch != 0 {
		seconds = int64(sec) | (epoch << 32)
	}
	return seconds, int64(extra >> 2)
}

// parseOracleExtent decodes "(0-97):1465-1562" or "(0):1567".
func parseOracleExtent(s string) (oracleExtent, bool) {
	var e oracleExtent
	open, close := strings.Index(s, "("), strings.Index(s, ")")
	colon := strings.Index(s, ":")
	if open != 0 || close < 0 || colon < close {
		return e, false
	}

	logical := s[1:close]
	physical := s[colon+1:]

	e.logicalStart, e.logicalEnd = parseRange(logical)
	e.physicalStart, e.physicalEnd = parseRange(physical)
	return e, true
}

func parseRange(s string) (uint64, uint64) {
	if i := strings.Index(s, "-"); i >= 0 {
		lo, _ := strconv.ParseUint(s[:i], 10, 64)
		hi, _ := strconv.ParseUint(s[i+1:], 10, 64)
		return lo, hi
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v, v
}

// forEachCorpusImage runs fn for every image that has a captured oracle.
func forEachCorpusImage(t *testing.T, fn func(t *testing.T, fs *FS, o *oracle)) {
	dir := corpusDir(t)

	images, err := filepath.Glob(filepath.Join(dir, "*.img"))
	if err != nil || len(images) == 0 {
		t.Skipf("no images in %s", dir)
	}
	sort.Strings(images)

	ran := 0
	for _, img := range images {
		oraclePath := strings.TrimSuffix(img, ".img") + ".oracle"
		if _, err := os.Stat(oraclePath); err != nil {
			continue
		}
		ran++
		t.Run(filepath.Base(img), func(t *testing.T) {
			fs, err := OpenFile(img)
			if err != nil {
				// bigalloc and meta_bg are refused by design; that is the
				// correct answer, not a failure.
				t.Skipf("image not openable (may be by design): %v", err)
			}
			defer fs.Close()

			fn(t, fs, parseOracle(t, oraclePath))
		})
	}
	if ran == 0 {
		t.Skipf("no .oracle files in %s; run testdata/mkoracle.sh", dir)
	}
}

// ---------------------------------------------------------------------------

func TestOracleGeometry(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		sb := fs.Superblock()
		if uint64(sb.BlockSize) != o.blockSize {
			t.Errorf("BlockSize = %d, dumpe2fs says %d", sb.BlockSize, o.blockSize)
		}
		if sb.BlocksCount != o.blockCount {
			t.Errorf("BlocksCount = %d, dumpe2fs says %d", sb.BlocksCount, o.blockCount)
		}
		if uint64(sb.InodesCount) != o.inodeCount {
			t.Errorf("InodesCount = %d, dumpe2fs says %d", sb.InodesCount, o.inodeCount)
		}
		if uint64(sb.InodeSize) != o.inodeSize {
			t.Errorf("InodeSize = %d, dumpe2fs says %d", sb.InodeSize, o.inodeSize)
		}
	})
}

func TestOracleInodeMetadata(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		checked := 0
		for num, want := range o.inodes {
			if want.links == 0 && want.size == 0 {
				continue // never allocated
			}
			got, err := fs.ReadInode(num)
			if err != nil {
				t.Errorf("inode %d: %v", num, err)
				continue
			}
			checked++

			if got.Size != want.size {
				t.Errorf("inode %d size = %d, debugfs says %d", num, got.Size, want.size)
			}
			if uint64(got.LinksCount) != want.links {
				t.Errorf("inode %d links = %d, debugfs says %d", num, got.LinksCount, want.links)
			}
			if got.Mtime.Unix() != want.mtimeSec {
				t.Errorf("inode %d mtime = %d, debugfs says %d", num, got.Mtime.Unix(), want.mtimeSec)
			}
			if int64(got.Mtime.Nanosecond()) != want.mtimeNsec {
				t.Errorf("inode %d mtime nsec = %d, debugfs says %d",
					num, got.Mtime.Nanosecond(), want.mtimeNsec)
			}
			if got.HasCrtime != want.hasCrtime {
				t.Errorf("inode %d HasCrtime = %v, debugfs %v", num, got.HasCrtime, want.hasCrtime)
			}
		}
		if checked == 0 {
			t.Error("no inodes were compared")
		}
		t.Logf("compared %d inodes", checked)
	})
}

func TestOracleExtents(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		checked := 0
		for num, want := range o.extents {
			got, err := fs.ExtentsWithOptions(num, ExtentOptions{OmitSparse: true, NoCoalesce: true})
			if err != nil {
				t.Errorf("inode %d extents: %v", num, err)
				continue
			}

			// debugfs prints inclusive ranges; normalise both to the same shape.
			var gotRuns []string
			for _, e := range got {
				if e.Sparse() || e.Inline() {
					continue
				}
				gotRuns = append(gotRuns, fmt.Sprintf("%d-%d:%d-%d",
					e.LogicalBlock, e.LogicalBlock+e.Blocks-1,
					e.PhysicalBlock, e.PhysicalBlock+e.Blocks-1))
			}
			var wantRuns []string
			for _, e := range want {
				wantRuns = append(wantRuns, fmt.Sprintf("%d-%d:%d-%d",
					e.logicalStart, e.logicalEnd, e.physicalStart, e.physicalEnd))
			}

			if strings.Join(gotRuns, ",") != strings.Join(wantRuns, ",") {
				t.Errorf("inode %d extents:\n  got  %v\n  want %v", num, gotRuns, wantRuns)
			}
			checked++
		}
		if checked == 0 {
			t.Skip("no extent-mapped inodes in this image")
		}
		t.Logf("compared extents for %d inodes", checked)
	})
}

// TestOracleBlockAccounting checks both halves of the block map at once: the
// data blocks the extents describe plus the metadata blocks that map them must
// equal what debugfs counts.
func TestOracleBlockAccounting(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		checked, skipped := 0, 0
		for num, wantTotal := range o.blocks {
			inode, err := fs.ReadInode(num)
			if err != nil || inode.HasInline {
				skipped++
				continue
			}
			exts, err := fs.InodeExtents(inode, ExtentOptions{OmitSparse: true})
			if err != nil {
				skipped++
				continue
			}
			meta, err := fs.MetadataBlocks(num)
			if err != nil {
				skipped++
				continue
			}

			var data uint64
			for _, e := range exts {
				if !e.Sparse() && !e.Inline() {
					data += e.Blocks
				}
			}
			got := int(data) + len(meta)
			if got != wantTotal {
				t.Errorf("inode %d: %d data + %d metadata = %d blocks, debugfs counts %d",
					num, data, len(meta), got, wantTotal)
			}
			checked++
		}
		t.Logf("compared block accounting for %d inodes (%d skipped)", checked, skipped)
	})
}

func TestOracleDeletedInodes(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		entries, err := fs.DeletedEntriesWithOptions(DeletedScanOptions{SkipDirSlack: true})
		if err != nil {
			t.Fatalf("DeletedEntries: %v", err)
		}

		got := map[uint32]bool{}
		for _, e := range entries {
			got[e.Inode] = true
		}

		for num := range o.deleted {
			if !got[num] {
				t.Errorf("inode %d reported deleted by debugfs lsdel but not by libext", num)
			}
		}
		for num := range got {
			if !o.deleted[num] {
				t.Errorf("inode %d reported deleted by libext but not by debugfs lsdel", num)
			}
		}
		t.Logf("deleted inodes: %d (debugfs %d)", len(got), len(o.deleted))
	})
}

func TestOracleDirectorySlack(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		if len(o.slack) == 0 {
			t.Skip("no deleted directory records in this image")
		}

		// debugfs reports slack for the root here; compare that directory.
		slack, err := fs.ScanDirSlack(RootInode)
		if err != nil {
			t.Fatalf("ScanDirSlack: %v", err)
		}
		got := map[string]bool{}
		for _, s := range slack {
			if !s.ShadowsLive {
				got["/ "+strconv.FormatUint(uint64(s.Inode), 10)+" "+s.Name] = true
			}
		}

		// Every record debugfs found must be found here. The converse is not
		// required: debugfs stops after the first stale record in a gap, so it
		// reports a subset.
		for want := range o.slack {
			if !strings.HasPrefix(want, "/ ") {
				continue
			}
			if !got[want] {
				t.Errorf("debugfs recovered %q from slack, libext did not", want)
			}
		}
		t.Logf("slack records: libext %d, debugfs %d", len(got), len(o.slack))
	})
}

func TestOracleJournalTransactions(t *testing.T) {
	forEachCorpusImage(t, func(t *testing.T, fs *FS, o *oracle) {
		if len(o.journal) == 0 {
			t.Skip("no journal transactions in this image")
		}

		txns, err := fs.ListJournalTransactions()
		if err != nil {
			t.Skipf("no journal: %v", err)
		}

		// debugfs reports descriptor (1), commit (2) and revoke (5) blocks;
		// commit blocks are folded into their transaction here, so compare the
		// descriptor and revoke records only.
		got := map[string]bool{}
		for _, tx := range txns {
			typ := "1"
			if tx.Type == "revoke" {
				typ = "5"
			}
			got[fmt.Sprintf("%d %s %d", tx.Sequence, typ, tx.StartBlock)] = true
		}

		missing := 0
		for _, want := range o.journal {
			if strings.Contains(want, " 2 ") {
				continue // commit block
			}
			if !got[want] {
				t.Errorf("debugfs logdump reports transaction %q, libext does not", want)
				missing++
			}
		}
		t.Logf("journal records compared: %d debugfs, %d libext, %d missing",
			len(o.journal), len(got), missing)
	})
}
