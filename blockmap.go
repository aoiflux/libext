package libext

import (
	"encoding/binary"
	"fmt"
)

const (
	extentHeaderMagic = 0xF30A
	extentHeaderSize  = 12
	extentEntrySize   = 12

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

func (fs *FS) inodeBlockNumber(inode Inode, logical uint64) (uint64, bool, error) {
	if inode.HasExtents || fs.kind == FSKindExt4 || (fs.sb.FeatureIncompat&featureIncompatExtents) != 0 {
		recs, err := fs.parseExtentTree(inode.BlockRaw[:])
		if err != nil {
			// The inode flag asserts this is an extent tree. Falling back to the
			// classic block map would reinterpret the extent header as block
			// pointers and return fabricated offsets, so report the failure.
			if inode.HasExtents {
				return 0, false, fmt.Errorf("inode %d extent tree: %w", inode.Number, err)
			}
		} else if len(recs) > 0 {
			for _, rec := range recs {
				start := uint64(rec.logicalStart)
				end := start + uint64(rec.length)
				if logical < start || logical >= end {
					continue
				}
				if rec.unwritten {
					return 0, false, nil
				}
				return rec.physical + (logical - start), true, nil
			}
			return 0, false, nil
		}
	}
	return fs.classicBlockNumber(inode.BlockRaw[:], logical)
}

func (fs *FS) classicBlockNumber(blockRaw []byte, logical uint64) (uint64, bool, error) {
	ptrs := make([]uint32, 15)
	for i := 0; i < 15; i++ {
		ptrs[i] = binary.LittleEndian.Uint32(blockRaw[i*4 : i*4+4])
	}

	entriesPerBlock := uint64(fs.sb.BlockSize / 4)
	if logical < 12 {
		p := ptrs[logical]
		if p == 0 {
			return 0, false, nil
		}
		return uint64(p), true, nil
	}

	logical -= 12
	if logical < entriesPerBlock {
		if ptrs[12] == 0 {
			return 0, false, nil
		}
		p, err := fs.readPointerFromBlock(uint64(ptrs[12]), logical)
		if err != nil {
			return 0, false, err
		}
		if p == 0 {
			return 0, false, nil
		}
		return uint64(p), true, nil
	}

	logical -= entriesPerBlock
	doubleSpan := entriesPerBlock * entriesPerBlock
	if logical < doubleSpan {
		if ptrs[13] == 0 {
			return 0, false, nil
		}
		l1 := logical / entriesPerBlock
		l2 := logical % entriesPerBlock
		p1, err := fs.readPointerFromBlock(uint64(ptrs[13]), l1)
		if err != nil {
			return 0, false, err
		}
		if p1 == 0 {
			return 0, false, nil
		}
		p2, err := fs.readPointerFromBlock(uint64(p1), l2)
		if err != nil {
			return 0, false, err
		}
		if p2 == 0 {
			return 0, false, nil
		}
		return uint64(p2), true, nil
	}

	logical -= doubleSpan
	tripleSpan := entriesPerBlock * entriesPerBlock * entriesPerBlock
	if logical >= tripleSpan {
		return 0, false, nil
	}
	if ptrs[14] == 0 {
		return 0, false, nil
	}

	l1 := logical / (entriesPerBlock * entriesPerBlock)
	rem := logical % (entriesPerBlock * entriesPerBlock)
	l2 := rem / entriesPerBlock
	l3 := rem % entriesPerBlock

	p1, err := fs.readPointerFromBlock(uint64(ptrs[14]), l1)
	if err != nil {
		return 0, false, err
	}
	if p1 == 0 {
		return 0, false, nil
	}
	p2, err := fs.readPointerFromBlock(uint64(p1), l2)
	if err != nil {
		return 0, false, err
	}
	if p2 == 0 {
		return 0, false, nil
	}
	p3, err := fs.readPointerFromBlock(uint64(p2), l3)
	if err != nil {
		return 0, false, err
	}
	if p3 == 0 {
		return 0, false, nil
	}
	return uint64(p3), true, nil
}

func (fs *FS) readPointerFromBlock(block uint64, idx uint64) (uint32, error) {
	blk, err := fs.readBlock(block)
	if err != nil {
		return 0, err
	}
	max := uint64(len(blk) / 4)
	if idx >= max {
		return 0, nil
	}
	off := idx * 4
	return binary.LittleEndian.Uint32(blk[off : off+4]), nil
}

// extentWalk carries the per-inode limits that bound a tree traversal.
type extentWalk struct {
	visited map[uint64]struct{}
	count   int
	limit   int
}

func (fs *FS) parseExtentTree(root []byte) ([]extentRecord, error) {
	w := &extentWalk{
		visited: make(map[uint64]struct{}),
		limit:   fs.maxExtents(),
	}
	return fs.parseExtentNode(root, -1, w)
}

// parseExtentNode parses one node of an extent tree.
//
// parentDepth is the depth recorded by the node that pointed here, or -1 for the
// root. A well-formed child is exactly one level shallower than its parent;
// enforcing that, together with the visited set, is what bounds the traversal.
// Without both, an index block pointing at itself recurses until the stack is
// exhausted, which no recover can catch.
func (fs *FS) parseExtentNode(node []byte, parentDepth int, w *extentWalk) ([]extentRecord, error) {
	if len(node) < extentHeaderSize {
		return nil, ErrUnsupportedLayout
	}
	// Every node carries the magic, not just the root.
	if le16(node, 0) != extentHeaderMagic {
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

	capacity := (len(node) - extentHeaderSize) / extentEntrySize
	if entries > capacity {
		return nil, fmt.Errorf("%w: extent node claims %d entries, node holds %d", ErrUnsupportedLayout, entries, capacity)
	}
	if max != 0 && entries > max {
		return nil, fmt.Errorf("%w: extent node claims %d entries, header max is %d", ErrUnsupportedLayout, entries, max)
	}

	if depth == 0 {
		recs := make([]extentRecord, 0, entries)
		for i := 0; i < entries; i++ {
			off := extentHeaderSize + i*extentEntrySize
			logical := le32(node, off)
			lenFlags := le16(node, off+4)
			unwritten := (lenFlags & 0x8000) != 0
			lenBlocks := uint32(lenFlags & 0x7FFF)
			if lenBlocks == 0 {
				continue
			}
			startHi := le16(node, off+6)
			startLo := le32(node, off+8)
			start := (uint64(startHi) << 32) | uint64(startLo)

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
		off := extentHeaderSize + i*extentEntrySize
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

		child, err := fs.readBlock(leaf)
		if err != nil {
			return nil, fmt.Errorf("read extent node block %d: %w", leaf, err)
		}
		leafRecs, err := fs.parseExtentNode(child, depth, w)
		if err != nil {
			return nil, err
		}
		recs = append(recs, leafRecs...)
	}
	return recs, nil
}
