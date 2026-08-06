package libext

import (
	"fmt"
	"slices"
	"sort"
)

// ExtentFlags describes the nature of one mapped run.
type ExtentFlags uint32

const (
	// ExtentSparse marks a hole: the run has no physical backing and reads as
	// zeros. PhysicalBlock is 0 and carries no meaning.
	ExtentSparse ExtentFlags = 1 << iota

	// ExtentUnwritten marks a preallocated run. It has a real physical location
	// but reads as zeros through the file interface. The location is reported
	// because the blocks may still hold whatever occupied them previously.
	ExtentUnwritten

	// ExtentInline marks data stored inside the inode rather than in blocks.
	// PhysicalBlock and Blocks carry no meaning; read the data with the inline
	// accessors instead.
	ExtentInline
)

func (f ExtentFlags) String() string {
	var parts []string
	if f&ExtentSparse != 0 {
		parts = append(parts, "sparse")
	}
	if f&ExtentUnwritten != 0 {
		parts = append(parts, "unwritten")
	}
	if f&ExtentInline != 0 {
		parts = append(parts, "inline")
	}
	if len(parts) == 0 {
		return "allocated"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "|" + p
	}
	return out
}

// Extent is one contiguous logical-to-physical run of blocks.
//
// PhysicalBlock is volume-relative: it is a block index within this filesystem,
// not an offset into the image the filesystem may be embedded in. Use DataRuns,
// or add Options.BaseOffset yourself, to obtain image-absolute positions.
//
// A slice of Extents returned by this package is sorted by LogicalBlock, covers
// the file without gaps up to its last logical block (holes appear as explicit
// ExtentSparse runs unless suppressed), and has physically adjacent runs merged.
// Classic block-mapped inodes and extent-mapped inodes produce the same shape,
// so callers never branch on which mapping scheme an inode uses.
type Extent struct {
	LogicalBlock  uint64
	PhysicalBlock uint64
	Blocks        uint64
	Flags         ExtentFlags
}

// Sparse reports whether the run is a hole with no physical backing.
func (e Extent) Sparse() bool { return e.Flags&ExtentSparse != 0 }

// Unwritten reports whether the run is preallocated: allocated on disk but
// reading as zeros.
func (e Extent) Unwritten() bool { return e.Flags&ExtentUnwritten != 0 }

// Inline reports whether the data lives in the inode rather than in blocks.
func (e Extent) Inline() bool { return e.Flags&ExtentInline != 0 }

// End returns the logical block one past the end of the run.
func (e Extent) End() uint64 { return e.LogicalBlock + e.Blocks }

// ByteRange is an Extent expressed in bytes, clamped to the file's size.
//
// DiskOffset includes Options.BaseOffset and is therefore image-absolute. It is
// 0 for sparse ranges, which have no location.
type ByteRange struct {
	FileOffset int64
	DiskOffset int64
	Length     int64
	Sparse     bool
	Unwritten  bool
}

// ExtentOptions tunes extent enumeration. The zero value is the default:
// holes are reported and adjacent runs are merged.
type ExtentOptions struct {
	// OmitSparse suppresses hole runs, leaving gaps in the logical coverage.
	OmitSparse bool

	// NoCoalesce keeps runs exactly as the on-disk structures record them
	// instead of merging physically adjacent ones. Use it to observe the real
	// fragmentation of an extent tree.
	NoCoalesce bool

	// MaxExtents caps the returned run count. 0 uses Options.MaxExtents.
	MaxExtents int
}

func (fs *FS) extentLimit(opts ExtentOptions) int {
	if opts.MaxExtents > 0 {
		return opts.MaxExtents
	}
	return fs.maxExtents()
}

// Extents returns the block map of an inode using default options.
func (fs *FS) Extents(inodeNum uint32) ([]Extent, error) {
	return fs.ExtentsWithOptions(inodeNum, ExtentOptions{})
}

// ExtentsWithOptions returns the block map of an inode.
func (fs *FS) ExtentsWithOptions(inodeNum uint32, opts ExtentOptions) ([]Extent, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	return fs.InodeExtents(inode, opts)
}

// InodeExtents returns the block map of an inode already read, avoiding a second
// trip to the inode table.
func (fs *FS) InodeExtents(inode Inode, opts ExtentOptions) ([]Extent, error) {
	// A fast symlink has a size but no blocks: its target is stored in the
	// block map itself. Normalizing an empty run list against that size would
	// invent a sparse extent for data that is not on disk at all.
	if isFastSymlink(inode) {
		return nil, nil
	}

	runs, _, err := fs.rawExtents(inode, opts)
	if err != nil {
		return nil, err
	}
	return fs.normalizeExtents(runs, fs.inodeBlockCount(inode), opts)
}

// isFastSymlink reports whether the inode stores its link target inline in the
// block pointer area rather than in a data block.
func isFastSymlink(inode Inode) bool {
	return inode.IsSymlink && inode.Size > 0 && inode.Size <= uint64(len(inode.BlockRaw))
}

// Extents returns the block map of the open file.
func (f *File) Extents() ([]Extent, error) {
	return f.volume.InodeExtents(f.inode, ExtentOptions{})
}

// MetadataBlocks returns the blocks an inode owns that hold mapping structures
// rather than file data: extent tree index blocks, or indirect block pointers.
// They are allocated to the file and count against it, but carrying them in the
// data map would corrupt any extraction built on it.
func (fs *FS) MetadataBlocks(inodeNum uint32) ([]uint64, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	_, meta, err := fs.rawExtents(inode, ExtentOptions{})
	if err != nil {
		return nil, err
	}
	slices.Sort(meta)
	return slices.Compact(meta), nil
}

// DataRuns returns the byte ranges of an inode, clamped to its size, with
// image-absolute disk offsets.
func (fs *FS) DataRuns(inodeNum uint32) ([]ByteRange, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	return fs.inodeDataRuns(inode)
}

// DataRuns returns the byte ranges of the open file.
func (f *File) DataRuns() ([]ByteRange, error) {
	return f.volume.inodeDataRuns(f.inode)
}

func (fs *FS) inodeDataRuns(inode Inode) ([]ByteRange, error) {
	exts, err := fs.InodeExtents(inode, ExtentOptions{})
	if err != nil {
		return nil, err
	}
	return fs.byteRanges(exts, inode.Size), nil
}

// byteRanges converts block runs to file-relative byte ranges, dropping anything
// past the end of the file. Blocks mapped beyond the file size exist (extents
// can describe preallocation past EOF) but have no byte range within the file;
// use Extents to see them.
func (fs *FS) byteRanges(exts []Extent, size uint64) []ByteRange {
	if size == 0 || fs.sb.BlockSize == 0 {
		return nil
	}
	blockSize := uint64(fs.sb.BlockSize)

	out := make([]ByteRange, 0, len(exts))
	for _, e := range exts {
		if e.Inline() {
			continue
		}
		fileOff := e.LogicalBlock * blockSize
		if fileOff >= size {
			break
		}
		length := e.Blocks * blockSize
		if fileOff+length > size {
			length = size - fileOff
		}

		var diskOff int64
		if !e.Sparse() {
			diskOff = int64(e.PhysicalBlock*blockSize) + fs.opts.BaseOffset
		}

		out = append(out, ByteRange{
			FileOffset: int64(fileOff),
			DiskOffset: diskOff,
			Length:     int64(length),
			Sparse:     e.Sparse(),
			Unwritten:  e.Unwritten(),
		})
	}
	return out
}

// inodeBlockCount is the number of logical blocks the file's size spans.
func (fs *FS) inodeBlockCount(inode Inode) uint64 {
	if fs.sb.BlockSize == 0 {
		return 0
	}
	blockSize := uint64(fs.sb.BlockSize)
	return (inode.Size + blockSize - 1) / blockSize
}

// rawExtents enumerates the mapped runs of an inode with holes not yet filled
// in, alongside the metadata blocks encountered on the way.
func (fs *FS) rawExtents(inode Inode, opts ExtentOptions) ([]Extent, []uint64, error) {
	// Under BIGALLOC an extent counts clusters, not blocks; every physical
	// offset derived here would be scaled wrongly. Open refuses such images
	// unless Permissive is set, so this guards the permissive path.
	if (fs.sb.FeatureROCompat & featureRoCompatBigalloc) != 0 {
		return nil, nil, fmt.Errorf("%w: BIGALLOC addresses clusters, not blocks", ErrUnsupportedLayout)
	}

	if (inode.Flags & inodeFlagInlineData) != 0 {
		return []Extent{{Flags: ExtentInline}}, nil, nil
	}

	if isFastSymlink(inode) {
		return nil, nil, nil
	}

	if inode.HasExtents || fs.kind == FSKindExt4 || (fs.sb.FeatureIncompat&featureIncompatExtents) != 0 {
		recs, meta, err := fs.parseExtentTreeWithMeta(inode.BlockRaw[:])
		if err == nil {
			runs := make([]Extent, 0, len(recs))
			for _, rec := range recs {
				var flags ExtentFlags
				if rec.unwritten {
					flags |= ExtentUnwritten
				}
				runs = append(runs, Extent{
					LogicalBlock:  uint64(rec.logicalStart),
					PhysicalBlock: rec.physical,
					Blocks:        uint64(rec.length),
					Flags:         flags,
				})
			}
			return runs, meta, nil
		}
		// The flag asserts this is an extent tree; reinterpreting the header as
		// block pointers would fabricate offsets.
		if inode.HasExtents {
			return nil, nil, fmt.Errorf("inode %d extent tree: %w", inode.Number, err)
		}
	}

	return fs.classicExtents(inode, fs.extentLimit(opts))
}

// classicExtents walks the ext2-style direct, indirect, double- and
// triple-indirect pointers.
//
// Each indirect block is read exactly once. The per-block lookup path reads an
// indirect block for every logical block it resolves, which is quadratic across
// a whole file; enumerating the map in one pass is what makes the read path
// linear.
func (fs *FS) classicExtents(inode Inode, limit int) ([]Extent, []uint64, error) {
	blockCount := fs.inodeBlockCount(inode)
	if blockCount == 0 {
		return nil, nil, nil
	}

	var (
		runs    []Extent
		meta    []uint64
		logical uint64
		cur     *Extent
	)

	emit := func(l, phys uint64) error {
		if phys == 0 {
			cur = nil // hole; normalization fills it in
			return nil
		}
		if cur != nil && cur.End() == l && cur.PhysicalBlock+cur.Blocks == phys {
			cur.Blocks++
			return nil
		}
		if len(runs) >= limit {
			return fmt.Errorf("%w: block map exceeds %d runs", ErrUnsupportedLayout, limit)
		}
		runs = append(runs, Extent{LogicalBlock: l, PhysicalBlock: phys, Blocks: 1})
		cur = &runs[len(runs)-1]
		return nil
	}

	ptrs := make([]uint32, 15)
	for i := 0; i < 15; i++ {
		ptrs[i] = le32(inode.BlockRaw[:], i*4)
	}

	// Twelve direct pointers.
	for i := 0; i < 12 && logical < blockCount; i++ {
		if err := emit(logical, uint64(ptrs[i])); err != nil {
			return nil, nil, err
		}
		logical++
	}

	// walkIndirect descends `level` tiers of pointer blocks.
	var walkIndirect func(block uint64, level int) error
	walkIndirect = func(block uint64, level int) error {
		if block == 0 || logical >= blockCount {
			return nil
		}
		if fs.sb.BlocksCount != 0 && block >= fs.sb.BlocksCount {
			return fmt.Errorf("%w: indirect block %d out of range", ErrUnsupportedLayout, block)
		}
		meta = append(meta, block)

		data, err := fs.readBlock(block)
		if err != nil {
			return fmt.Errorf("read indirect block %d: %w", block, err)
		}
		entries := len(data) / 4

		for i := 0; i < entries && logical < blockCount; i++ {
			p := uint64(le32(data, i*4))
			if level == 1 {
				if err := emit(logical, p); err != nil {
					return err
				}
				logical++
				continue
			}
			if p == 0 {
				// A missing subtree still advances the logical position by the
				// span it would have covered.
				span := spanOfLevel(uint64(entries), level-1)
				logical += span
				cur = nil
				continue
			}
			if err := walkIndirect(p, level-1); err != nil {
				return err
			}
		}
		return nil
	}

	// Walked in order: the logical cursor advances as we go, so the three
	// indirection tiers must be visited in sequence.
	for _, step := range []struct {
		ptr   uint32
		level int
	}{
		{ptrs[12], 1},
		{ptrs[13], 2},
		{ptrs[14], 3},
	} {
		if logical >= blockCount {
			break
		}
		if step.ptr == 0 {
			logical += spanOfLevel(uint64(fs.sb.BlockSize)/4, step.level)
			cur = nil
			continue
		}
		if err := walkIndirect(uint64(step.ptr), step.level); err != nil {
			return nil, nil, err
		}
	}

	return runs, meta, nil
}

// spanOfLevel is how many data blocks a pointer at the given indirection level
// covers: entries^level.
func spanOfLevel(entries uint64, level int) uint64 {
	span := uint64(1)
	for i := 0; i < level; i++ {
		span *= entries
	}
	return span
}

// normalizeExtents sorts runs, rejects or clips overlaps, fills holes, and
// merges adjacent runs, so that every mapping scheme yields the same shape.
func (fs *FS) normalizeExtents(runs []Extent, blockCount uint64, opts ExtentOptions) ([]Extent, error) {
	if len(runs) == 1 && runs[0].Inline() {
		return runs, nil
	}

	slices.SortFunc(runs, func(a, b Extent) int {
		switch {
		case a.LogicalBlock < b.LogicalBlock:
			return -1
		case a.LogicalBlock > b.LogicalBlock:
			return 1
		default:
			return 0
		}
	})

	limit := fs.extentLimit(opts)
	out := make([]Extent, 0, len(runs)+1)

	appendRun := func(e Extent) error {
		if e.Blocks == 0 {
			return nil
		}
		if !opts.NoCoalesce && len(out) > 0 {
			last := &out[len(out)-1]
			if last.Flags == e.Flags && last.End() == e.LogicalBlock &&
				(e.Sparse() || last.PhysicalBlock+last.Blocks == e.PhysicalBlock) {
				last.Blocks += e.Blocks
				return nil
			}
		}
		if len(out) >= limit {
			return fmt.Errorf("%w: block map exceeds %d runs", ErrUnsupportedLayout, limit)
		}
		out = append(out, e)
		return nil
	}

	var cursor uint64
	for _, r := range runs {
		if r.LogicalBlock < cursor {
			overlap := cursor - r.LogicalBlock
			detail := fmt.Sprintf("extent at logical block %d overlaps the previous run by %d blocks",
				r.LogicalBlock, overlap)
			if !fs.opts.Permissive {
				return nil, fmt.Errorf("%w: %s", ErrUnsupportedLayout, detail)
			}
			fs.warn(WarnDegradedRead, "", detail+"; clipped")
			if overlap >= r.Blocks {
				continue
			}
			r.LogicalBlock += overlap
			if !r.Sparse() {
				r.PhysicalBlock += overlap
			}
			r.Blocks -= overlap
		}

		if r.LogicalBlock > cursor && !opts.OmitSparse {
			if err := appendRun(Extent{
				LogicalBlock: cursor,
				Blocks:       r.LogicalBlock - cursor,
				Flags:        ExtentSparse,
			}); err != nil {
				return nil, err
			}
		}
		if err := appendRun(r); err != nil {
			return nil, err
		}
		cursor = r.End()
	}

	// A file whose tail is a hole ends with no run covering it.
	if cursor < blockCount && !opts.OmitSparse {
		if err := appendRun(Extent{
			LogicalBlock: cursor,
			Blocks:       blockCount - cursor,
			Flags:        ExtentSparse,
		}); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// lookupExtent finds the run covering a logical block. The slice must be sorted,
// which normalizeExtents guarantees.
func lookupExtent(exts []Extent, logical uint64) (Extent, bool) {
	i := sort.Search(len(exts), func(i int) bool {
		return exts[i].End() > logical
	})
	if i == len(exts) || exts[i].LogicalBlock > logical {
		return Extent{}, false
	}
	return exts[i], true
}
