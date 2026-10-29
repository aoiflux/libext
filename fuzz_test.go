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

		// The tree walk and the index build are the two recursions in the library
		// that a crafted image can drive: a directory reachable from itself, or a
		// chain deeper than any real filesystem. Both carry a cycle guard and a
		// depth cap, and both belong under the same budget as everything else.
		timed("WalkDir", func() {
			_ = fs.WalkDir(RootInode, func(string, DirEntry) error { return nil })
		})
		timed("WalkDirWithInode", func() {
			_ = fs.WalkDirWithInode(RootInode, func(string, DirEntry, Inode) error { return nil })
		})
		timed("BuildPathIndex", func() { _, _ = fs.BuildPathIndex() })
		timed("BuildJournalIndex", func() {
			// A crafted journal superblock can claim any number of blocks and any
			// tag layout, so the index build is as much an untrusted-input parser
			// as the walk is.
			if jx, err := fs.BuildJournalIndex(); err == nil {
				_, _ = jx.BlockCopies(0)
				_, _ = jx.InodeVersions(RootInode)
			}
		})
		timed("PathFor", func() { _, _ = fs.PathFor(RootInode) })

		// Strict adds its own reads - tree block tails, directory tails, every
		// named inode - and must hold to the same budget.
		sfs, err := OpenWithOptions(bytes.NewReader(img), Options{Strict: true, Permissive: true})
		if err != nil {
			return
		}
		defer sfs.Close()
		timed("Strict WalkDirWithInode", func() {
			_ = sfs.WalkDirWithInode(RootInode, func(_ string, _ DirEntry, inode Inode) error {
				if inode.IsRegular {
					_, _ = sfs.ReadFile(inode.Number)
				}
				return nil
			})
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

		// So must the strict walk, which also checks each block's tail.
		fs.opts.Strict = true
		fs.sb.FeatureROCompat = ROCompatMetadataCsum
		inode := Inode{Number: 12, Generation: 7}
		copy(inode.BlockRaw[:], root)
		_, _, _ = fs.parseInodeExtentTree(inode)
	})
}

// FuzzTimeCodec checks that every timestamp an inode can hold survives a
// decode and re-encode unchanged, so a writer using EncodeTime reproduces what
// a reader saw.
func FuzzTimeCodec(f *testing.F) {
	f.Add(uint32(0x80000000), uint32(1))
	f.Add(uint32(0x7FFFFFFF), uint32(3|999999999<<2))
	f.Add(uint32(0), uint32(0))

	f.Fuzz(func(t *testing.T, sec, extra uint32) {
		if extra>>2 >= 1e9 {
			return // not a nanosecond count the kernel writes
		}
		at := DecodeTime(sec, extra)
		gs, ge := EncodeTime(at)
		if gs != sec || ge != extra {
			t.Fatalf("(0x%08x, 0x%08x) decodes as %s, re-encodes as (0x%08x, 0x%08x)",
				sec, extra, at, gs, ge)
		}
	})
}

func FuzzParseDirEntries(f *testing.F) {
	f.Add(dirent(2, 12, FileTypeDir, "."))
	f.Add(damagedDirData())
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, permissive := range []bool{false, true} {
			fs := &FS{
				sb:   Superblock{FeatureIncompat: IncompatFiletype, BlockSize: 1024},
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
		binary.LittleEndian.PutUint16(raw[InodeOffMode:], ModeRegular|0o644)
		binary.LittleEndian.PutUint16(raw[InodeOffExtraISize:], 32)
	}))
	f.Add(make([]byte, 128))

	f.Fuzz(func(t *testing.T, raw []byte) {
		// parseInode indexes fixed offsets; anything shorter than the base inode
		// is the caller's responsibility, so mirror that guard here.
		if len(raw) < GoodOldInodeSize {
			return
		}
		inode := parseInode(raw, 12)
		_ = inode.Timestamps()
		_ = inode.Deleted()
		_, _ = parseInodeXAttrs(raw, inode.ExtraISize)
	})
}

// FuzzInlineInode drives the inline-data assembler over a patched inode.
//
// FuzzOpen reaches InlineData only through the root, which is a directory, so
// its size is the low word alone and the 64-bit claim behind PAR-35 was out of
// reach. This target forces the inline flag on, so a regular file's
// i_size_high is in scope.
func FuzzInlineInode(f *testing.F) {
	f.Add(buildInodeWithXAttrs(f, ModeRegular|0o644, 19, InodeFlagInlineData,
		[]byte("tiny inline content"), nil))
	f.Add(buildInodeWithXAttrs(f, ModeRegular|0o644, 128, InodeFlagInlineData,
		bytes.Repeat([]byte("H"), inlineDataInBlock), []inodeXAttr{
			{index: xattrIndexSystem, name: inlineDataAttrName,
				value: bytes.Repeat([]byte("T"), 68)},
		}))
	f.Add(forgedInlineInode(f))
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, raw []byte) {
		// parseInode indexes fixed offsets, so anything shorter than the base
		// inode is the caller's responsibility, as in FuzzParseInode.
		if len(raw) < GoodOldInodeSize {
			return
		}
		// The flag is what puts an inode on this path at all, so it is forced
		// rather than left to the mutation: an input that cleared it would
		// exercise nothing.
		raw = append([]byte(nil), raw...)
		binary.LittleEndian.PutUint32(raw[InodeOffFlags:],
			le32(raw, InodeOffFlags)|InodeFlagInlineData)

		inode := parseInode(raw, 12)

		start := time.Now()
		data, err := (&FS{}).inlineDataFromRaw(inode, raw)
		if elapsed := time.Since(start); elapsed > fuzzOpBudget {
			t.Fatalf("inlineDataFromRaw took %v on a %d byte inode, over the %v budget",
				elapsed, len(raw), fuzzOpBudget)
		}

		// Whatever comes back has to fit the inode it came from. More than
		// that would mean the length was taken from the claimed size rather
		// than from the inode.
		if max := maxInlineDataSize(len(raw), inode.ExtraISize); len(data) > max {
			t.Fatalf("returned %d bytes from a %d byte inode holding at most %d (err=%v)",
				len(data), len(raw), max, err)
		}
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

// TestUninitBitmapAllocationIsBounded is PAR-35's mistake in another place: a
// group's bitmap was sized from s_blocks_per_group, which the superblock only
// checks non-zero. A bitmap occupies at most one block, which is the clamp the
// read path already applied; the uninitialised path did not, so a crafted
// superblock could ask for up to 512 MiB per group before reading anything.
func TestUninitBitmapAllocationIsBounded(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.blocksPerGroup = 1 << 20 // 128 KiB of bitmap; the field permits 512 MiB
	img := buildTestImage(t, cfg)

	// BLOCK_UNINIT is the path that allocates without reading a block first.
	binary.LittleEndian.PutUint16(img[testGroupDescOffset+GdOffFlags:], GroupBlockUninit)

	fs := openFixture(t, img, Options{})

	bitmap, err := fs.BlockBitmap(0)
	if err != nil {
		t.Fatalf("BlockBitmap: %v", err)
	}
	if max := int(fs.Superblock().BlockSize); len(bitmap) > max {
		t.Errorf("uninitialised block bitmap is %d bytes; one bitmap occupies at most %d",
			len(bitmap), max)
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
