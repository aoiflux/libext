package libext

// On-disk structure layout.
//
// Every byte offset into an ext structure is named here rather than written
// inline at its use site. Two reasons, both learned from this package's own
// history: a transposed offset produces a plausible wrong value rather than an
// error, and the same field is often read from more than one place, so an
// unnamed offset can be corrected in one and left wrong in the other.
//
// Offsets are grouped by the structure they belong to and ordered as they
// appear on disk. All ext structures are little-endian; the journal is
// big-endian and its offsets live in journal.go alongside the code that knows
// to byte-swap.

// Superblock field offsets, relative to the start of the superblock (which
// itself begins at SuperblockOffset within the volume).
const (
	sbOffInodesCount      = 0x00
	sbOffBlocksCountLo    = 0x04
	sbOffReservedBlocksLo = 0x08
	sbOffFreeBlocksLo     = 0x0C
	sbOffFreeInodes       = 0x10
	sbOffFirstDataBlock   = 0x14
	sbOffLogBlockSize     = 0x18
	sbOffLogClusterSize   = 0x1C
	sbOffBlocksPerGroup   = 0x20
	sbOffClustersPerGroup = 0x24
	sbOffInodesPerGroup   = 0x28
	sbOffMountTime        = 0x2C
	sbOffWriteTime        = 0x30
	sbOffMountCount       = 0x34
	sbOffMaxMountCount    = 0x36
	sbOffMagic            = 0x38
	sbOffState            = 0x3A
	sbOffErrorsBehavior   = 0x3C
	sbOffCreatorOS        = 0x48
	sbOffRevisionLevel    = 0x4C
	sbOffFirstInode       = 0x54
	sbOffInodeSize        = 0x58
	sbOffFeatureCompat    = 0x5C
	sbOffFeatureIncompat  = 0x60
	sbOffFeatureROCompat  = 0x64
	sbOffUUID             = 0x68
	sbOffVolumeName       = 0x78
	sbOffLastMounted      = 0x88
	sbOffJournalInode     = 0xE0
	sbOffJournalDevice    = 0xE4
	sbOffLastOrphan       = 0xE8
	sbOffDescSize         = 0xFE
	sbOffBlocksCountHi    = 0x150
	sbOffReservedBlocksHi = 0x154
	sbOffFreeBlocksHi     = 0x158
	sbOffOverheadClusters = 0x248
	sbOffChecksumSeed     = 0x270
	sbOffOrphanFileInode  = 0x280
	sbOffChecksum         = 0x3FC
)

// Superblock field widths, where a field is a fixed-size array rather than an
// integer.
const (
	sbUUIDLen        = 16
	sbVolumeNameLen  = 16
	sbLastMountedLen = 64
)

// Group descriptor field offsets. The first 32 bytes exist in every ext
// revision; the remainder only when the descriptor is 64 bytes wide.
const (
	gdOffBlockBitmapLo   = 0x00
	gdOffInodeBitmapLo   = 0x04
	gdOffInodeTableLo    = 0x08
	gdOffFreeBlocksLo    = 0x0C
	gdOffFreeInodesLo    = 0x0E
	gdOffUsedDirsLo      = 0x10
	gdOffFlags           = 0x12
	gdOffExcludeBitmapLo = 0x14
	gdOffBlockBitmapCSLo = 0x18
	gdOffInodeBitmapCSLo = 0x1A
	gdOffItableUnusedLo  = 0x1C
	gdOffChecksum        = 0x1E
	gdOffBlockBitmapHi   = 0x20
	gdOffInodeBitmapHi   = 0x24
	gdOffInodeTableHi    = 0x28
	gdOffFreeBlocksHi    = 0x2C
	gdOffFreeInodesHi    = 0x2E
	gdOffUsedDirsHi      = 0x30
	gdOffItableUnusedHi  = 0x32
	gdOffExcludeBitmapHi = 0x34
	gdOffBlockBitmapCSHi = 0x38
	gdOffInodeBitmapCSHi = 0x3A
)

// Group descriptor sizes.
const (
	// gdSizeMin is the descriptor size on a filesystem without the 64BIT
	// feature, and the minimum any filesystem may declare.
	gdSizeMin = 32
	// gdSize64Bit is the descriptor size once 64BIT is set, at which point the
	// high halves and the second checksum pair become meaningful.
	gdSize64Bit = 64
)

// Inode checksum field offsets. The low half lives in osd2; the high half only
// exists when i_extra_isize reaches far enough to cover it.
const (
	inodeOffChecksumLo = 0x7C
	inodeOffChecksumHi = 0x82
	// inodeChecksumHiEnd is the first byte past i_checksum_hi. An inode carries
	// the high half only when its extra area extends at least this far.
	inodeChecksumHiEnd = 0x84
)

// Directory entry layout.
const (
	// dirEntryHeaderSize is inode(4) + rec_len(2) + name_len(1) + file_type(1).
	dirEntryHeaderSize = 8
	// dirEntryMaxNameLen is the longest name a single record can hold.
	dirEntryMaxNameLen = 255
	// dirEntryFileTypeMax is the highest defined file type code; anything above
	// it marks a record as implausible during slack recovery.
	dirEntryFileTypeMax = 7
)

// Character range checks used when judging whether a byte run recovered from
// directory slack can be a filename.
const (
	// asciiSpace is the lowest printable byte; anything below it is a control
	// character, which no ordinary tool writes into a name.
	asciiSpace = 0x20
	// asciiDelete is the one non-printable byte above the printable range.
	asciiDelete = 0x7F
)

// Extended attribute layout, shared by the in-inode area and the external
// block. The two differ only in where entries start and what value offsets are
// measured from.
const (
	// xattrEntryNameLenOff and its neighbours index one ext4_xattr_entry.
	xattrEntryNameLenOff   = 0
	xattrEntryNameIndexOff = 1
	xattrEntryValueOffsOff = 2
	xattrEntryValueInumOff = 4
	xattrEntryValueSizeOff = 8
	xattrEntryHashOff      = 12
	// xattrEntryAlign is the boundary each entry is padded to.
	xattrEntryAlign = 4
)

// Bit manipulation constants for allocation bitmaps.
const (
	// bitsPerByte is used to convert between a bit index and its byte.
	bitsPerByte = 8
)

// Scan pacing.
const (
	// cancellationCheckInterval is how many inodes a scan processes between
	// context checks. Checking every iteration costs more than it saves; a
	// group is already a bounded unit, so this only bounds the tail latency of
	// a cancel within one group.
	cancellationCheckInterval = 1024
)
