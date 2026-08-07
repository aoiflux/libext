package libext

import "fmt"

// Allocation bitmaps.
//
// The bitmaps answer the question a deleted-file scan actually turns on: not
// "did this inode once exist" but "have its blocks been handed to something
// else since". They are also what keeps a scan honest — a group flagged
// uninitialised holds pre-existing disk contents, not deleted files.

// bitmapKind distinguishes the two allocation bitmaps a group owns. It exists
// so the cache and the read path can be written once instead of twice.
type bitmapKind uint8

const (
	blockBitmapKind bitmapKind = iota
	inodeBitmapKind
)

func (k bitmapKind) String() string {
	if k == inodeBitmapKind {
		return "inode"
	}
	return "block"
}

// BlockBitmap returns the raw allocation bitmap for a block group.
//
// The result covers BlocksPerGroup bits. A group flagged BlockUninit has no
// meaningful bitmap on disk; an all-zero bitmap is returned for it, since none
// of its blocks are in use.
//
// The returned slice is cached and shared between callers. Treat it as
// read-only: bitmaps do not change while an image is open, and copying one per
// call would defeat the cache that makes a full-image scan affordable.
func (fs *FS) BlockBitmap(group uint32) ([]byte, error) {
	return fs.cachedBitmap(group, blockBitmapKind)
}

// InodeBitmap returns the raw allocation bitmap for a block group.
//
// The result covers InodesPerGroup bits. A group flagged InodeUninit returns an
// all-zero bitmap: its inode table has never been written, so nothing in it is
// allocated regardless of what the bytes on disk happen to say.
//
// The same caching and read-only caveat as BlockBitmap applies.
func (fs *FS) InodeBitmap(group uint32) ([]byte, error) {
	return fs.cachedBitmap(group, inodeBitmapKind)
}

// cachedBitmap returns a group's bitmap, reading it at most once per image.
//
// Judging whether a deleted file's blocks have been reused needs one bitmap
// lookup per block, so without a cache a single large file re-reads the same
// bitmap thousands of times. The cache is shared across goroutines under an
// RWMutex; a bitmap is immutable once read, so concurrent readers never block
// each other after the first miss.
func (fs *FS) cachedBitmap(group uint32, kind bitmapKind) ([]byte, error) {
	fs.bitmapMu.RLock()
	cache := fs.blockBitmap
	if kind == inodeBitmapKind {
		cache = fs.inodeBitmap
	}
	bitmap, ok := cache[group]
	fs.bitmapMu.RUnlock()
	if ok {
		return bitmap, nil
	}

	bitmap, err := fs.loadBitmap(group, kind)
	if err != nil {
		return nil, err
	}

	fs.bitmapMu.Lock()
	defer fs.bitmapMu.Unlock()

	// Another goroutine may have populated the entry while this one was
	// reading. Prefer the stored slice so every caller shares one copy.
	if kind == inodeBitmapKind {
		if fs.inodeBitmap == nil {
			fs.inodeBitmap = make(map[uint32][]byte)
		}
		if existing, ok := fs.inodeBitmap[group]; ok {
			return existing, nil
		}
		fs.inodeBitmap[group] = bitmap
		return bitmap, nil
	}

	if fs.blockBitmap == nil {
		fs.blockBitmap = make(map[uint32][]byte)
	}
	if existing, ok := fs.blockBitmap[group]; ok {
		return existing, nil
	}
	fs.blockBitmap[group] = bitmap
	return bitmap, nil
}

// loadBitmap reads one bitmap from disk, honouring the uninitialised flags.
func (fs *FS) loadBitmap(group uint32, kind bitmapKind) ([]byte, error) {
	gd, err := fs.group(group)
	if err != nil {
		return nil, err
	}

	var (
		bits   uint32
		block  uint64
		uninit bool
	)
	if kind == inodeBitmapKind {
		bits, block, uninit = fs.sb.InodesPerGroup, gd.InodeBitmapBlock, gd.InodeUninit()
	} else {
		bits, block, uninit = fs.sb.BlocksPerGroup, gd.BlockBitmapBlock, gd.BlockUninit()
	}

	size := int((bits + bitsPerByte - 1) / bitsPerByte)
	if uninit {
		return make([]byte, size), nil
	}
	return fs.readBitmap(block, size, kind.String(), group)
}

func (fs *FS) group(group uint32) (GroupDescriptor, error) {
	if group >= uint32(len(fs.groups)) {
		return GroupDescriptor{}, fmt.Errorf("%w: group %d of %d", ErrInvalidInode, group, len(fs.groups))
	}
	return fs.groups[group], nil
}

// readBitmap reads size bytes of a bitmap, which may span less than a block.
func (fs *FS) readBitmap(block uint64, size int, kind string, group uint32) ([]byte, error) {
	if block == 0 || (fs.sb.BlocksCount != 0 && block >= fs.sb.BlocksCount) {
		return nil, fmt.Errorf("%w: group %d %s bitmap at block %d", ErrUnsupportedLayout, group, kind, block)
	}
	blk, err := fs.readBlock(block)
	if err != nil {
		return nil, fmt.Errorf("read group %d %s bitmap: %w", group, kind, err)
	}
	if size > len(blk) {
		size = len(blk)
	}
	return blk[:size], nil
}

// InodeAllocated reports whether the inode bitmap still marks an inode in use.
//
// For a deleted inode this is the difference between "the metadata survives and
// nothing has reused it" and "the slot has already been handed to a new file".
func (fs *FS) InodeAllocated(inodeNum uint32) (bool, error) {
	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return false, ErrInvalidInode
	}
	group := (inodeNum - 1) / fs.sb.InodesPerGroup
	index := (inodeNum - 1) % fs.sb.InodesPerGroup

	bitmap, err := fs.InodeBitmap(group)
	if err != nil {
		return false, err
	}
	return bitTest(bitmap, uint64(index)), nil
}

// BlockAllocated reports whether a block is marked in use.
func (fs *FS) BlockAllocated(block uint64) (bool, error) {
	first := uint64(fs.sb.FirstDataBlock)
	if block < first || (fs.sb.BlocksCount != 0 && block >= fs.sb.BlocksCount) {
		return false, fmt.Errorf("%w: block %d outside the filesystem", ErrUnsupportedLayout, block)
	}
	rel := block - first
	group := uint32(rel / uint64(fs.sb.BlocksPerGroup))
	index := rel % uint64(fs.sb.BlocksPerGroup)

	bitmap, err := fs.BlockBitmap(group)
	if err != nil {
		return false, err
	}
	return bitTest(bitmap, index), nil
}

// bitTest reads bit n of a little-endian allocation bitmap.
func bitTest(bitmap []byte, n uint64) bool {
	byteIdx := n / 8
	if byteIdx >= uint64(len(bitmap)) {
		return false
	}
	return bitmap[byteIdx]&(1<<(n%8)) != 0
}

// usableInodesInGroup is the number of inode table entries worth examining: the
// tail counted by bg_itable_unused has never been written, so reading it yields
// whatever the disk held beforehand.
func (fs *FS) usableInodesInGroup(gd GroupDescriptor) uint32 {
	if gd.InodeUninit() {
		return 0
	}
	if gd.ItableUnused >= fs.sb.InodesPerGroup {
		return 0
	}
	return fs.sb.InodesPerGroup - gd.ItableUnused
}
