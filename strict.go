package libext

import "fmt"

// StrictCheck names one of the checks Options.Strict adds.
type StrictCheck string

const (
	// StrictExtentTail: an extent tree block's checksum tail is missing or
	// wrong.
	StrictExtentTail StrictCheck = "extent_tail"
	// StrictExtentRange: a leaf extent runs past the end of the filesystem.
	StrictExtentRange StrictCheck = "extent_range"
	// StrictDirTail: a directory leaf block's checksum tail is missing or wrong.
	StrictDirTail StrictCheck = "dir_tail"
	// StrictDuplicateName: one directory holds two entries with the same name.
	StrictDuplicateName StrictCheck = "duplicate_name"
	// StrictHole: an inode being read has a hole within its size.
	StrictHole StrictCheck = "hole"
	// StrictUnwritten: an inode being read has an unwritten run within its size.
	StrictUnwritten StrictCheck = "unwritten"
	// StrictShortMap: an inode maps fewer blocks than its size needs.
	StrictShortMap StrictCheck = "short_map"
	// StrictUnlinked: a named entry's inode has a link count of zero.
	StrictUnlinked StrictCheck = "unlinked"
	// StrictDeleted: a named entry's inode carries a deletion time.
	StrictDeleted StrictCheck = "deleted"
	// StrictBlockMapped: an inode on an extents filesystem uses block pointers.
	StrictBlockMapped StrictCheck = "block_mapped"
)

// StrictError reports an image that Options.Strict refused. It wraps
// ErrStrict, and also ErrChecksumMismatch when the failed check is a checksum,
// so errors.Is answers either question.
type StrictError struct {
	Check StrictCheck
	// Inode is the inode the check concerns, or 0 when none does.
	Inode uint32
	// Block is the filesystem block the check concerns, or 0 when none does or
	// it is not known.
	Block  uint64
	Detail string

	checksum bool
}

func (e *StrictError) Error() string {
	msg := fmt.Sprintf("%s: %s", ErrStrict, e.Check)
	if e.Inode != 0 {
		msg += fmt.Sprintf(": inode %d", e.Inode)
	}
	if e.Block != 0 {
		msg += fmt.Sprintf(" block %d", e.Block)
	}
	return msg + ": " + e.Detail
}

func (e *StrictError) Unwrap() []error {
	if e.checksum {
		return []error{ErrStrict, ErrChecksumMismatch}
	}
	return []error{ErrStrict}
}

func strictErr(check StrictCheck, inode uint32, block uint64, format string, args ...any) error {
	return &StrictError{Check: check, Inode: inode, Block: block, Detail: fmt.Sprintf(format, args...)}
}

// strictReadable reports whether Strict's mapping checks apply to an inode:
// a regular file, a directory or a slow symlink, whose data lives in blocks.
func (fs *FS) strictReadable(inode Inode) bool {
	if !inode.IsRegular && !inode.IsDirectory && !inode.IsSymlink {
		return false
	}
	return !inode.HasInline && !fs.isFastSymlink(inode)
}

// strictCheckInode applies the per-inode check that needs no block map: on an
// extents filesystem the kernel gives every new regular file, directory and
// slow symlink the EXTENTS flag (__ext4_new_inode).
func (fs *FS) strictCheckInode(inode Inode) error {
	if !fs.opts.Strict || !fs.strictReadable(inode) {
		return nil
	}
	if fs.sb.FeatureIncompat&IncompatExtents != 0 && !inode.HasExtents {
		return strictErr(StrictBlockMapped, inode.Number, 0,
			"maps its blocks with block pointers on a filesystem with the extents feature")
	}
	return nil
}

// strictCheckMap rejects a block map that leaves part of the inode's size
// without written data: a hole, an unwritten run, or a map that stops short.
func (fs *FS) strictCheckMap(inode Inode, exts []Extent) error {
	if !fs.opts.Strict || !fs.strictReadable(inode) {
		return nil
	}
	if err := fs.strictCheckInode(inode); err != nil {
		return err
	}
	need := fs.inodeBlockCount(inode)
	var mapped uint64
	for i, e := range exts {
		if e.LogicalBlock >= need {
			break
		}
		switch {
		case e.Unwritten():
			return strictErr(StrictUnwritten, inode.Number, e.PhysicalBlock,
				"unwritten run of %d blocks at logical block %d", e.Blocks, e.LogicalBlock)
		case e.Sparse() && e.End() >= need && i == len(exts)-1:
			// A trailing hole is a map that stops short; reported below.
		case e.Sparse():
			return strictErr(StrictHole, inode.Number, 0,
				"hole of %d blocks at logical block %d", e.Blocks, e.LogicalBlock)
		default:
			mapped += min(e.End(), need) - e.LogicalBlock
		}
	}
	if mapped < need {
		return strictErr(StrictShortMap, inode.Number, 0,
			"%d bytes need %d blocks, %d are mapped", inode.Size, need, mapped)
	}
	return nil
}

// strictCheckDirBlocks verifies the checksum tail that ends each directory
// leaf block on a metadata_csum filesystem. exts is the map data was read
// through, which names the physical block in an error.
//
// htree index blocks carry a different tail (struct dx_tail) and are skipped,
// recognised as the kernel's __ext4_read_dirblock recognises them: on an
// indexed directory, block 0, and any block whose first record spans it whole.
func (fs *FS) strictCheckDirBlocks(inode Inode, data []byte, exts []Extent) error {
	if !fs.opts.Strict || inode.HasInline || !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	bs := int(fs.sb.BlockSize)
	seed := fs.csumSeed()
	for b := 0; b*bs < len(data); b++ {
		if b*bs+bs > len(data) {
			return strictErr(StrictDirTail, inode.Number, dirPhysicalBlock(exts, b),
				"directory size %d is not a whole number of blocks", inode.Size)
		}
		blk := data[b*bs : b*bs+bs]
		if inode.Flags&InodeFlagIndex != 0 && (b == 0 || dirRecLen(blk, bs) == bs) {
			continue
		}
		t := blk[bs-DirTailSize:]
		if le32(t, 0) != 0 || le16(t, 4) != DirTailSize || t[6] != 0 || t[7] != DirTailFileType {
			return strictErr(StrictDirTail, inode.Number, dirPhysicalBlock(exts, b),
				"logical block %d has no checksum tail", b)
		}
		stored := le32(t, 8)
		if calc := DirTailChecksum(seed, inode.Number, inode.Generation, blk); calc != stored {
			e := strictErr(StrictDirTail, inode.Number, dirPhysicalBlock(exts, b),
				"logical block %d stored=0x%08x calc=0x%08x", b, stored, calc).(*StrictError)
			e.checksum = true
			return e
		}
	}
	return nil
}

// dirRecLen decodes the first record length of a directory block, which a
// 64 KiB block cannot hold in 16 bits (ext4_rec_len_from_disk).
func dirRecLen(blk []byte, bs int) int {
	n := int(le16(blk, 4))
	if bs >= 1<<16 && (n == 0 || n == 0xFFFF) {
		return bs
	}
	return n
}

// dirPhysicalBlock locates a directory's logical block in its map for an
// error report, or returns 0 when it cannot.
func dirPhysicalBlock(exts []Extent, logical int) uint64 {
	e, ok := lookupExtent(exts, uint64(logical))
	if !ok || e.Sparse() {
		return 0
	}
	return e.PhysicalBlock + uint64(logical) - e.LogicalBlock
}

// strictCheckNames rejects a directory listing two entries with one name.
func (fs *FS) strictCheckNames(dir uint32, entries []DirEntry) error {
	if !fs.opts.Strict {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if _, dup := seen[e.Name]; dup {
			return strictErr(StrictDuplicateName, dir, 0, "two entries named %q", e.Name)
		}
		seen[e.Name] = struct{}{}
	}
	return nil
}

// strictCheckNamed rejects a directory entry whose inode is not live.
func (fs *FS) strictCheckNamed(name string, inode Inode) error {
	if !fs.opts.Strict {
		return nil
	}
	if inode.LinksCount == 0 {
		return strictErr(StrictUnlinked, inode.Number, 0, "%s names an inode with no links", name)
	}
	if inode.DtimeRaw != 0 {
		return strictErr(StrictDeleted, inode.Number, 0, "%s names an inode with deletion time %d", name, inode.DtimeRaw)
	}
	return fs.strictCheckInode(inode)
}
