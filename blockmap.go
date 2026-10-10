package libext

import (
	"fmt"
)

const (
	// extentMaxDepth bounds extent tree depth. The on-disk format permits at
	// most 5 levels; anything deeper is corruption or a crafted image.
	extentMaxDepth = 5

	// defaultMaxExtents caps the records parsed for one inode when
	// Options.MaxExtents is unset.
	defaultMaxExtents = 1 << 20
)

type extentRecord struct {
	logicalStart uint32
	length       uint32
	physical     uint64
	unwritten    bool
}

func (fs *FS) maxExtents() int {
	if fs.opts.MaxExtents > 0 {
		return fs.opts.MaxExtents
	}
	return defaultMaxExtents
}

// extentWalk carries the per-inode limits that bound a tree traversal, and
// records the index blocks visited so they can be reported as metadata.
//
// strict, when set, is the inode the tree belongs to, and turns on the checks
// Options.Strict adds: each tree block's checksum tail, and leaf extents that
// run past the end of the filesystem. A tree parsed without its inode cannot
// have its tails checked, because the checksum is seeded with the inode.
type extentWalk struct {
	visited map[uint64]struct{}
	index   []uint64
	count   int
	limit   int
	strict  *Inode
}

func (fs *FS) parseExtentTree(root []byte) ([]extentRecord, error) {
	recs, _, err := fs.parseExtentTreeWithMeta(root)
	return recs, err
}

// parseExtentTreeWithMeta parses a tree and also returns the index blocks it
// traversed. Those blocks belong to the file but hold mapping structures rather
// than data.
func (fs *FS) parseExtentTreeWithMeta(root []byte) ([]extentRecord, []uint64, error) {
	return fs.walkExtentTree(root, nil)
}

// parseInodeExtentTree parses an inode's extent tree, applying Options.Strict.
func (fs *FS) parseInodeExtentTree(inode Inode) ([]extentRecord, []uint64, error) {
	var strict *Inode
	if fs.opts.Strict {
		strict = &inode
	}
	return fs.walkExtentTree(inode.BlockRaw[:], strict)
}

func (fs *FS) walkExtentTree(root []byte, strict *Inode) ([]extentRecord, []uint64, error) {
	w := &extentWalk{
		visited: make(map[uint64]struct{}),
		limit:   fs.maxExtents(),
		strict:  strict,
	}
	recs, err := fs.parseExtentNode(root, -1, w)
	if err != nil {
		return nil, nil, err
	}
	return recs, w.index, nil
}

// parseExtentNode parses one node of an extent tree.
//
// parentDepth is the depth recorded by the node that pointed here, or -1 for the
// root. A well-formed child is exactly one level shallower than its parent;
// enforcing that, together with the visited set, is what bounds the traversal.
// Without both, an index block pointing at itself recurses until the stack is
// exhausted, which no recover can catch.
func (fs *FS) parseExtentNode(node []byte, parentDepth int, w *extentWalk) ([]extentRecord, error) {
	if len(node) < ExtentHeaderSize {
		return nil, ErrUnsupportedLayout
	}
	// Every node carries the magic, not just the root.
	if le16(node, 0) != ExtentMagic {
		return nil, fmt.Errorf("%w: bad extent node magic 0x%04x", ErrUnsupportedLayout, le16(node, 0))
	}

	entries := int(le16(node, 2))
	max := int(le16(node, 4))
	depth := int(le16(node, 6))

	if depth > extentMaxDepth {
		return nil, fmt.Errorf("%w: extent tree depth %d exceeds limit %d", ErrUnsupportedLayout, depth, extentMaxDepth)
	}
	if parentDepth >= 0 && depth != parentDepth-1 {
		return nil, fmt.Errorf("%w: extent node depth %d under parent depth %d", ErrUnsupportedLayout, depth, parentDepth)
	}

	capacity := (len(node) - ExtentHeaderSize) / ExtentEntrySize
	if entries > capacity {
		return nil, fmt.Errorf("%w: extent node claims %d entries, node holds %d", ErrUnsupportedLayout, entries, capacity)
	}
	if max != 0 && entries > max {
		return nil, fmt.Errorf("%w: extent node claims %d entries, header max is %d", ErrUnsupportedLayout, entries, max)
	}

	if depth == 0 {
		recs := make([]extentRecord, 0, entries)
		for i := 0; i < entries; i++ {
			off := ExtentHeaderSize + i*ExtentEntrySize
			logical := le32(node, off)
			// ee_len is not a flag bit over a 15-bit length. Up to and including
			// EXT_INIT_MAX_LEN it is a written extent of that many blocks; above
			// it, an unwritten extent of ee_len - EXT_INIT_MAX_LEN blocks
			// (ext4_ext_is_unwritten, ext4_ext_get_actual_len). Masking read a
			// written 32768-block run as unwritten and empty, and dropped it.
			lenBlocks := uint32(le16(node, off+4))
			unwritten := lenBlocks > ExtentInitMaxLen
			if unwritten {
				lenBlocks -= ExtentInitMaxLen
			}
			if lenBlocks == 0 {
				continue
			}
			startHi := le16(node, off+6)
			startLo := le32(node, off+8)
			start := (uint64(startHi) << 32) | uint64(startLo)
			if w.strict != nil && start+uint64(lenBlocks) > fs.sb.BlocksCount {
				return nil, strictErr(StrictExtentRange, w.strict.Number, start,
					"extent of %d blocks at logical block %d runs past the %d-block filesystem",
					lenBlocks, logical, fs.sb.BlocksCount)
			}

			if w.count >= w.limit {
				return nil, fmt.Errorf("%w: extent count exceeds limit %d", ErrUnsupportedLayout, w.limit)
			}
			w.count++

			recs = append(recs, extentRecord{
				logicalStart: logical,
				length:       lenBlocks,
				physical:     start,
				unwritten:    unwritten,
			})
		}
		return recs, nil
	}

	recs := make([]extentRecord, 0, entries*8)
	for i := 0; i < entries; i++ {
		off := ExtentHeaderSize + i*ExtentEntrySize
		leafLo := le32(node, off+4)
		leafHi := le16(node, off+8)
		leaf := (uint64(leafHi) << 32) | uint64(leafLo)

		if leaf == 0 || (fs.sb.BlocksCount != 0 && leaf >= fs.sb.BlocksCount) {
			return nil, fmt.Errorf("%w: extent index block %d out of range", ErrUnsupportedLayout, leaf)
		}
		if _, seen := w.visited[leaf]; seen {
			return nil, fmt.Errorf("%w: extent index block %d revisited", ErrUnsupportedLayout, leaf)
		}
		w.visited[leaf] = struct{}{}
		w.index = append(w.index, leaf)

		child, err := fs.readBlock(leaf)
		if err != nil {
			return nil, fmt.Errorf("read extent node block %d: %w", leaf, err)
		}
		if err := fs.strictCheckExtentTail(child, leaf, w); err != nil {
			return nil, err
		}
		leafRecs, err := fs.parseExtentNode(child, depth, w)
		if err != nil {
			return nil, err
		}
		recs = append(recs, leafRecs...)
	}
	return recs, nil
}

// strictCheckExtentTail verifies the checksum tail of an extent tree block
// under Options.Strict on a metadata_csum filesystem. A block with a bad magic
// is left for parseExtentNode to reject as malformed.
func (fs *FS) strictCheckExtentTail(block []byte, num uint64, w *extentWalk) error {
	if w.strict == nil || !fs.shouldValidateMetadataChecksums() {
		return nil
	}
	if len(block) < ExtentHeaderSize || le16(block, 0) != ExtentMagic {
		return nil
	}
	ino := w.strict
	off, ok := extentTailOffset(block)
	if !ok {
		return strictErr(StrictExtentTail, ino.Number, num,
			"eh_max %d leaves no room for the checksum tail", le16(block, 4))
	}
	stored := le32(block, off)
	if calc := ExtentTailChecksum(fs.csumSeed(), ino.Number, ino.Generation, block); calc != stored {
		e := strictErr(StrictExtentTail, ino.Number, num, "stored=0x%08x calc=0x%08x", stored, calc).(*StrictError)
		e.checksum = true
		return e
	}
	return nil
}
