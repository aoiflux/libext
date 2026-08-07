package libext

import "fmt"

// Allocation bitmaps.
//
// The bitmaps answer the question a deleted-file scan actually turns on: not
// "did this inode once exist" but "have its blocks been handed to something
// else since". They are also what keeps a scan honest — a group flagged
// uninitialised holds pre-existing disk contents, not deleted files.

// BlockBitmap returns the raw allocation bitmap for a block group.
//
// The result covers BlocksPerGroup bits. A group flagged BlockUninit has no
// meaningful bitmap on disk; an all-zero bitmap is returned for it, since none
// of its blocks are in use.
func (fs *FS) BlockBitmap(group uint32) ([]byte, error) {
	gd, err := fs.group(group)
	if err != nil {
		return nil, err
	}
	size := int((fs.sb.BlocksPerGroup + 7) / 8)
	if gd.BlockUninit() {
		return make([]byte, size), nil
	}
	return fs.readBitmap(gd.BlockBitmapBlock, size, "block", group)
}

// InodeBitmap returns the raw allocation bitmap for a block group.
//
// The result covers InodesPerGroup bits. A group flagged InodeUninit returns an
// all-zero bitmap: its inode table has never been written, so nothing in it is
// allocated regardless of what the bytes on disk happen to say.
func (fs *FS) InodeBitmap(group uint32) ([]byte, error) {
	gd, err := fs.group(group)
	if err != nil {
		return nil, err
	}
	size := int((fs.sb.InodesPerGroup + 7) / 8)
	if gd.InodeUninit() {
		return make([]byte, size), nil
	}
	return fs.readBitmap(gd.InodeBitmapBlock, size, "inode", group)
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
