package libext

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// Fuzz targets.
//
// These exist to discharge the panic-free obligation rather than to find
// interesting filesystems: every entry point must return an error on malformed
// input instead of panicking, recursing without bound, or allocating without
// bound. Run them with, for example:
//
//	go test -run xxx -fuzz FuzzOpen -fuzztime 60s
//
// The seeds are the fixtures the rest of the suite uses, so the corpus starts
// from structures that are already almost valid; that is where the interesting
// mutations are.

// fuzzOpBudget is how long any single operation may take on a fuzzed image.
// Generous enough that ordinary slowness passes, tight enough that a
// quadratic or unbounded path is caught rather than merely being slow.
const fuzzOpBudget = 5 * time.Second

func FuzzOpen(f *testing.F) {
	f.Add(buildTestImage(f, defaultSBConfig()))
	f.Add(buildDirFixture(f))
	f.Add(buildDeletedFixture(f))
	f.Add(make([]byte, 4096))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, img []byte) {
		fs, err := OpenWithOptions(bytes.NewReader(img), Options{Permissive: true})
		if err != nil {
			return
		}
		defer fs.Close()

		// Exercise the read paths an integrator would reach for. Any of these
		// may fail; none may panic, and none may run unboundedly: a parser fed
		// hostile input has to terminate, not merely avoid crashing.
		timed := func(name string, fn func()) {
			start := time.Now()
			fn()
			if elapsed := time.Since(start); elapsed > fuzzOpBudget {
				t.Fatalf("%s took %v on a %d byte image, over the %v budget",
					name, elapsed, len(img), fuzzOpBudget)
			}
		}

		timed("Superblock", func() { _ = fs.Superblock() })
		timed("GroupDescriptors", func() { _ = fs.GroupDescriptors() })
		timed("ListDir", func() { _, _ = fs.ListDir(RootInode) })
		timed("Extents", func() { _, _ = fs.Extents(RootInode) })
		timed("DataRuns", func() { _, _ = fs.DataRuns(RootInode) })
		timed("MetadataBlocks", func() { _, _ = fs.MetadataBlocks(RootInode) })
		timed("ReadInode", func() { _, _ = fs.ReadInode(RootInode) })
		timed("ScanDirSlack", func() { _, _ = fs.ScanDirSlack(RootInode) })
		timed("OrphanInodes", func() { _, _ = fs.OrphanInodes() })
		timed("InlineData", func() { _, _, _ = fs.InlineData(RootInode) })
		timed("GetXAttrs", func() { _, _ = fs.GetXAttrs(RootInode) })
		timed("DeletedEntries", func() {
			_, _ = fs.DeletedEntriesWithOptions(DeletedScanOptions{MaxResults: 16})
		})
	})
}

func FuzzParseExtentTree(f *testing.F) {
	f.Add(inodeRoot(extentHeader(1, 4, 0), extentLeafEntry(0, 2, 100)))
	f.Add(inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 5)))
	f.Add(inodeRoot(extentHeader(340, 340, 0)))
	f.Add(make([]byte, 60))

	f.Fuzz(func(t *testing.T, root []byte) {
		fs := newExtentFS(map[uint64][]byte{
			5: append(extentHeader(1, 84, 0), extentLeafEntry(0, 1, 20)...),
			9: bytes.Repeat([]byte{0xFF}, 1024),
		})
		// A self-referencing tree must terminate rather than exhaust the stack.
		_, _ = fs.parseExtentTree(root)
	})
}

func FuzzParseDirEntries(f *testing.F) {
	f.Add(dirent(2, 12, extDirentTypeDirectory, "."))
	f.Add(damagedDirData())
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, permissive := range []bool{false, true} {
			fs := &FS{
				sb:   Superblock{FeatureIncompat: featureIncompatFileType, BlockSize: 1024},
				opts: Options{Permissive: permissive},
			}
			entries, _ := fs.parseDirEntries(data)
			// Whatever comes back must be self-consistent.
			for _, e := range entries {
				if int(e.NameLen) != len(e.Name) {
					t.Fatalf("NameLen %d does not match name %q", e.NameLen, e.Name)
				}
			}
			_ = fs.scanDirBlockSlack(2, data, 0)
		}
	})
}

func FuzzParseXAttrBlock(f *testing.F) {
	valid := make([]byte, 256)
	binary.LittleEndian.PutUint32(valid[0:], xattrInodeMagic)
	valid[xattrBlockHeaderSize] = 4
	valid[xattrBlockHeaderSize+1] = xattrNamespaceUser
	binary.LittleEndian.PutUint16(valid[xattrBlockHeaderSize+2:], 100)
	binary.LittleEndian.PutUint32(valid[xattrBlockHeaderSize+8:], 5)
	copy(valid[xattrBlockHeaderSize+16:], "test")
	copy(valid[100:], "value")

	f.Add(valid)
	f.Add(make([]byte, 32))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseXAttrBlock(data)
		_, _ = parseXAttrEntries(data, 0, 0, len(data))
	})
}

func FuzzParseInode(f *testing.F) {
	f.Add(rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	}))
	f.Add(make([]byte, 128))

	f.Fuzz(func(t *testing.T, raw []byte) {
		// parseInode indexes fixed offsets; anything shorter than the base inode
		// is the caller's responsibility, so mirror that guard here.
		if len(raw) < inodeBaseSize {
			return
		}
		inode := parseInode(raw, 12)
		_ = inode.Timestamps()
		_ = inode.Deleted()
		_, _ = parseInodeXAttrs(raw, inode.ExtraISize)
	})
}

func FuzzParseJournal(f *testing.F) {
	f.Add(jbd2Superblock(4, 2, 0))
	f.Add(jbd2Descriptor(2, []uint64{100, 200}))
	f.Add(jbd2Commit(2, 0x69e58838, 1))
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, block []byte) {
		if jsb, err := ParseJournalSuperblock(block); err == nil {
			_ = parseJournalTags(block, jsb, 0)
		}
		_ = parseJournalTags(block, &JournalSuperblock{}, 0)
		_ = parseJournalTags(block, &JournalSuperblock{IncompatFeatures: journalIncompatCSumV3}, 0)
		_ = parseCommitTime(block)
		_ = parseFastCommitBlock(block, 0)
		_, _ = parseOrphanBlock(block)
	})
}

// ---------------------------------------------------------------------------
// resource ceilings
// ---------------------------------------------------------------------------

// TestCraftedGeometryDoesNotAllocate guards B1 from the other direction: the
// superblock is refused before any per-group allocation, so a claimed group
// count in the billions costs nothing.
func TestCraftedGeometryDoesNotAllocate(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.blocksCount = 1 << 42
	cfg.blocksPerGroup = 1
	img := buildTestImage(t, cfg)

	done := make(chan error, 1)
	go func() {
		_, err := Open(bytes.NewReader(img))
		done <- err
	}()

	if err := <-done; err == nil {
		t.Fatal("Open accepted a superblock describing 2^42 groups")
	}
}

// TestExtentDepthIsBounded is the stack-exhaustion guard from the other side:
// a cycle among index blocks must be refused rather than recursed.
func TestExtentDepthIsBounded(t *testing.T) {
	blocks := map[uint64][]byte{}
	// A chain of index blocks each pointing at the next, ending in a loop.
	for i := uint64(5); i < 12; i++ {
		next := i + 1
		if next == 12 {
			next = 5 // close the loop
		}
		blocks[i] = append(extentHeader(1, 84, 1), extentIndexEntry(0, next)...)
	}
	fs := newExtentFS(blocks)

	root := inodeRoot(extentHeader(1, 4, 2), extentIndexEntry(0, 5))
	if _, err := fs.parseExtentTree(root); err == nil {
		t.Fatal("a cyclic extent tree was accepted")
	}
}
