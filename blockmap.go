package libext

import (
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

// extentWalk carries the per-inode limits that bound a tree traversal, and
// records the index blocks visited so they can be reported as metadata.
type extentWalk struct {
	visited map[uint64]struct{}
	index   []uint64
	count   int
	limit   int
}

func (fs *FS) parseExtentTree(root []byte) ([]extentRecord, error) {
	recs, _, err := fs.parseExtentTreeWithMeta(root)
	return recs, err
}

// parseExtentTreeWithMeta parses a tree and also returns the index blocks it
// traversed. Those blocks belong to the file but hold mapping structures rather
// than data.
func (fs *FS) parseExtentTreeWithMeta(root []byte) ([]extentRecord, []uint64, error) {
	w := &extentWalk{
		visited: make(map[uint64]struct{}),
		limit:   fs.maxExtents(),
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
		w.index = append(w.index, leaf)

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
