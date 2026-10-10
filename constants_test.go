package libext

import "testing"

// exportedFeatureBits names the exported constant for every feature AllFeatures
// describes. The table and the constants are two statements of the same facts;
// this test is what stops them drifting apart.
var exportedFeatureBits = map[string]map[string]uint32{
	"compat": {
		"DIR_PREALLOC":   CompatDirPrealloc,
		"IMAGIC_INODES":  CompatImagicInodes,
		"HAS_JOURNAL":    CompatHasJournal,
		"EXT_ATTR":       CompatExtAttr,
		"RESIZE_INODE":   CompatResizeInode,
		"DIR_INDEX":      CompatDirIndex,
		"LAZY_BG":        CompatLazyBG,
		"EXCLUDE_INODE":  CompatExcludeInode,
		"EXCLUDE_BITMAP": CompatExcludeBitmap,
		"SPARSE_SUPER2":  CompatSparseSuper2,
		"FAST_COMMIT":    CompatFastCommit,
		"STABLE_INODES":  CompatStableInodes,
		"ORPHAN_FILE":    CompatOrphanFile,
	},
	"incompat": {
		"COMPRESSION": IncompatCompression,
		"FILETYPE":    IncompatFiletype,
		"RECOVER":     IncompatRecover,
		"JOURNAL_DEV": IncompatJournalDev,
		"META_BG":     IncompatMetaBG,
		"EXTENTS":     IncompatExtents,
		"64BIT":       Incompat64Bit,
		"MMP":         IncompatMMP,
		"FLEX_BG":     IncompatFlexBG,
		"EA_INODE":    IncompatEAInode,
		"DIRDATA":     IncompatDirData,
		"CSUM_SEED":   IncompatCSumSeed,
		"LARGEDIR":    IncompatLargedir,
		"INLINE_DATA": IncompatInlineData,
		"ENCRYPT":     IncompatEncrypt,
		"CASEFOLD":    IncompatCasefold,
	},
	"ro_compat": {
		"SPARSE_SUPER":   ROCompatSparseSuper,
		"LARGE_FILE":     ROCompatLargeFile,
		"BTREE_DIR":      ROCompatBtreeDir,
		"HUGE_FILE":      ROCompatHugeFile,
		"GDT_CSUM":       ROCompatGdtCsum,
		"DIR_NLINK":      ROCompatDirNlink,
		"EXTRA_ISIZE":    ROCompatExtraIsize,
		"HAS_SNAPSHOT":   ROCompatHasSnapshot,
		"QUOTA":          ROCompatQuota,
		"BIGALLOC":       ROCompatBigalloc,
		"METADATA_CSUM":  ROCompatMetadataCsum,
		"REPLICA":        ROCompatReplica,
		"READONLY":       ROCompatReadonly,
		"PROJECT":        ROCompatProject,
		"SHARED_BLOCKS":  ROCompatSharedBlocks,
		"VERITY":         ROCompatVerity,
		"ORPHAN_PRESENT": ROCompatOrphanPresent,
	},
}

func TestExportedFeatureBitsMatchFeatureTable(t *testing.T) {
	seen := 0
	for _, f := range AllFeatures {
		bits, ok := exportedFeatureBits[f.FlagType]
		if !ok {
			t.Errorf("feature %s has unknown flag type %q", f.Name, f.FlagType)
			continue
		}
		want, ok := bits[f.Name]
		if !ok {
			t.Errorf("%s feature %s has no exported constant", f.FlagType, f.Name)
			continue
		}
		if f.FlagValue != want {
			t.Errorf("%s feature %s: table says 0x%x, constant says 0x%x", f.FlagType, f.Name, f.FlagValue, want)
		}
		seen++
	}
	total := 0
	for _, bits := range exportedFeatureBits {
		total += len(bits)
	}
	if seen != total {
		t.Errorf("feature table describes %d features, %d constants are exported", seen, total)
	}
}

// The fixed values below are the kernel's (fs/ext4/ext4.h, ext4_extents.h).
// They are checked rather than trusted because a writer lays out an image
// from them: a wrong one produces an image the kernel rejects.
func TestExportedLayoutConstantsMatchKernel(t *testing.T) {
	checks := []struct {
		name      string
		got, want int
	}{
		{"BlockMapSize", BlockMapSize, 60},
		{"InlineSymlinkMax", InlineSymlinkMax, 59},
		{"ExtentInitMaxLen", ExtentInitMaxLen, 32768},
		{"ExtentHeaderSize", ExtentHeaderSize, 12},
		{"DirTailSize", DirTailSize, 12},
		{"SbOffChecksum", SbOffChecksum, 0x3FC},
		{"SbOffReserved+94*4", SbOffReserved + 94*4, SbOffChecksum},
		{"SbOffJnlBlocks+17*4", SbOffJnlBlocks + 17*4, SbOffBlocksCountHi},
		{"SbOffLastMounted+LastMountedLen", SbOffLastMounted + LastMountedLen, SbOffAlgorithmUsage},
		{"InodeOffBlockRaw+BlockMapSize", InodeOffBlockRaw + BlockMapSize, InodeOffGeneration},
		{"InodeOffChecksumHi+2", InodeOffChecksumHi + 2, inodeChecksumHiEnd},
		// i_extra_isize is 32 on a 256-byte inode made by mke2fs: everything up
		// to and including i_projid.
		{"InodeOffProjectID+4-GoodOldInodeSize", InodeOffProjectID + 4 - GoodOldInodeSize, 32},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
