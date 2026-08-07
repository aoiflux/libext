package libext

import "fmt"

// Orphan inode enumeration.
//
// An orphan is an inode that was unlinked while still open. Until the last
// handle closes, its blocks stay allocated and it belongs to no directory, so
// nothing in the tree references it. ext4 records these in one of two places,
// and which one depends on the filesystem's features rather than its state.

const (
	// orphanFileMagic marks each block of the ext4 orphan file.
	orphanFileMagic = 0x0b10ca04

	// orphanChainLimit bounds the legacy linked list. The chain lives in the
	// i_dtime field of each orphan, so a corrupt filesystem can produce a cycle.
	orphanChainLimit = 65536
)

// OrphanInodes returns the inodes ext4 recorded as unlinked-but-open.
//
// Both storage schemes are consulted. On kernels with the orphan_file feature
// the legacy s_last_orphan chain is normally empty and the real list lives in a
// dedicated file, so reading only the chain — as most tooling does — silently
// reports nothing on a modern filesystem.
func (fs *FS) OrphanInodes() ([]uint32, error) {
	inodes, _, err := fs.orphanInodesWithSource()
	return inodes, err
}

// orphanInodesWithSource returns orphan inodes alongside where each was found.
func (fs *FS) orphanInodesWithSource() ([]uint32, []DeletedSource, error) {
	var (
		inodes  []uint32
		sources []DeletedSource
		seen    = make(map[uint32]bool)
	)

	add := func(num uint32, src DeletedSource) {
		if num == 0 || num > fs.sb.InodesCount || seen[num] {
			return
		}
		seen[num] = true
		inodes = append(inodes, num)
		sources = append(sources, src)
	}

	for _, num := range fs.legacyOrphanChain() {
		add(num, DeletedSourceOrphanList)
	}

	fileOrphans, err := fs.orphanFileInodes()
	if err != nil {
		// The orphan file being unreadable should not suppress the chain.
		fs.warn(WarnDegradedRead, "orphan_file", err.Error())
	}
	for _, num := range fileOrphans {
		add(num, DeletedSourceOrphanFile)
	}

	return inodes, sources, nil
}

// legacyOrphanChain walks the s_last_orphan linked list.
//
// The link is stored in each orphan's i_dtime field, which is why a member of
// this list has no usable deletion time.
func (fs *FS) legacyOrphanChain() []uint32 {
	var (
		out  []uint32
		seen = make(map[uint32]bool)
	)

	for num := fs.sb.LastOrphan; num != 0 && len(out) < orphanChainLimit; {
		if num > fs.sb.InodesCount || seen[num] {
			fs.warn(WarnDegradedRead, "orphan_list",
				fmt.Sprintf("orphan chain broken at inode %d", num))
			break
		}
		seen[num] = true
		out = append(out, num)

		inode, err := fs.ReadInode(num)
		if err != nil {
			fs.warn(WarnDegradedRead, "orphan_list",
				fmt.Sprintf("orphan chain unreadable at inode %d: %v", num, err))
			break
		}
		num = inode.DtimeRaw
	}
	return out
}

// OrphanFileInode returns the inode backing the ext4 orphan file, or 0 when the
// filesystem does not use one.
func (fs *FS) OrphanFileInode() uint32 {
	if (fs.sb.FeatureCompat & featureCompatOrphanFile) == 0 {
		return 0
	}
	return fs.sb.OrphanFileInode
}

// orphanFileInodes reads the orphan file's block-per-block inode arrays.
//
// Each block holds a run of 32-bit inode numbers, zero meaning an empty slot,
// followed by an 8-byte tail carrying a magic and a checksum.
func (fs *FS) orphanFileInodes() ([]uint32, error) {
	num := fs.OrphanFileInode()
	if num == 0 {
		return nil, nil
	}

	inode, err := fs.ReadInode(num)
	if err != nil {
		return nil, fmt.Errorf("read orphan file inode %d: %w", num, err)
	}
	if inode.Size == 0 {
		return nil, nil
	}

	exts, err := fs.InodeExtents(inode, ExtentOptions{OmitSparse: true})
	if err != nil {
		return nil, fmt.Errorf("orphan file block map: %w", err)
	}

	var out []uint32
	for _, e := range exts {
		if e.Sparse() || e.Inline() {
			continue
		}
		for b := uint64(0); b < e.Blocks; b++ {
			block, err := fs.readBlock(e.PhysicalBlock + b)
			if err != nil {
				return out, fmt.Errorf("read orphan file block: %w", err)
			}
			entries, ok := parseOrphanBlock(block)
			if !ok {
				// A block without the magic is not part of the list.
				continue
			}
			out = append(out, entries...)
		}
	}
	return out, nil
}

// parseOrphanBlock extracts the inode numbers from one orphan file block.
func parseOrphanBlock(block []byte) ([]uint32, bool) {
	// The tail is the last eight bytes: magic then checksum.
	if len(block) < 8 {
		return nil, false
	}
	tail := len(block) - 8
	if le32(block, tail) != orphanFileMagic {
		return nil, false
	}

	var out []uint32
	for off := 0; off+4 <= tail; off += 4 {
		if num := le32(block, off); num != 0 {
			out = append(out, num)
		}
	}
	return out, true
}
