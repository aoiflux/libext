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
// itself begins at SuperblockOffset within the volume). This is the whole of
// struct ext4_super_block in fs/ext4/ext4.h, in order, so a writer can lay out
// every field from it; this package reads only some of them. Each field is a
// little-endian __le32 unless its comment gives another width or says it is
// an array.
const (
	SbOffInodesCount       = 0x00
	SbOffBlocksCountLo     = 0x04
	SbOffReservedBlocksLo  = 0x08
	SbOffFreeBlocksLo      = 0x0C
	SbOffFreeInodes        = 0x10
	SbOffFirstDataBlock    = 0x14
	SbOffLogBlockSize      = 0x18
	SbOffLogClusterSize    = 0x1C
	SbOffBlocksPerGroup    = 0x20
	SbOffClustersPerGroup  = 0x24
	SbOffInodesPerGroup    = 0x28
	SbOffMountTime         = 0x2C
	SbOffWriteTime         = 0x30
	SbOffMountCount        = 0x34 // __le16
	SbOffMaxMountCount     = 0x36 // __le16
	SbOffMagic             = 0x38 // __le16
	SbOffState             = 0x3A // __le16
	SbOffErrorsBehavior    = 0x3C // __le16
	SbOffMinorRevLevel     = 0x3E // __le16
	SbOffLastCheck         = 0x40
	SbOffCheckInterval     = 0x44
	SbOffCreatorOS         = 0x48
	SbOffRevisionLevel     = 0x4C
	SbOffDefResUID         = 0x50 // __le16
	SbOffDefResGID         = 0x52 // __le16
	SbOffFirstInode        = 0x54
	SbOffInodeSize         = 0x58 // __le16
	SbOffBlockGroupNr      = 0x5A // __le16
	SbOffFeatureCompat     = 0x5C
	SbOffFeatureIncompat   = 0x60
	SbOffFeatureROCompat   = 0x64
	SbOffUUID              = 0x68 // [UUIDLen]byte
	SbOffVolumeName        = 0x78 // [VolumeNameLen]byte
	SbOffLastMounted       = 0x88 // [LastMountedLen]byte
	SbOffAlgorithmUsage    = 0xC8
	SbOffPreallocBlocks    = 0xCC // __u8
	SbOffPreallocDirBlocks = 0xCD // __u8
	SbOffReservedGdtBlocks = 0xCE // __le16
	SbOffJournalUUID       = 0xD0 // [UUIDLen]byte
	SbOffJournalInode      = 0xE0
	SbOffJournalDevice     = 0xE4
	SbOffLastOrphan        = 0xE8
	SbOffHashSeed          = 0xEC // [4]__le32
	SbOffDefHashVersion    = 0xFC // __u8
	SbOffJnlBackupType     = 0xFD // __u8
	SbOffDescSize          = 0xFE // __le16
	SbOffDefaultMountOpts  = 0x100
	SbOffFirstMetaBG       = 0x104
	SbOffMkfsTime          = 0x108
	SbOffJnlBlocks         = 0x10C // [17]__le32
	SbOffBlocksCountHi     = 0x150
	SbOffReservedBlocksHi  = 0x154
	SbOffFreeBlocksHi      = 0x158
	SbOffMinExtraIsize     = 0x15C // __le16
	SbOffWantExtraIsize    = 0x15E // __le16
	SbOffFlags             = 0x160
	SbOffRaidStride        = 0x164 // __le16
	SbOffMMPUpdateInterval = 0x166 // __le16
	SbOffMMPBlock          = 0x168 // __le64
	SbOffRaidStripeWidth   = 0x170
	SbOffLogGroupsPerFlex  = 0x174 // __u8
	SbOffChecksumType      = 0x175 // __u8
	SbOffEncryptionLevel   = 0x176 // __u8
	SbOffKbytesWritten     = 0x178 // __le64
	SbOffSnapshotInum      = 0x180
	SbOffSnapshotID        = 0x184
	SbOffSnapshotRBlocks   = 0x188 // __le64
	SbOffSnapshotList      = 0x190
	SbOffErrorCount        = 0x194
	SbOffFirstErrorTime    = 0x198
	SbOffFirstErrorIno     = 0x19C
	SbOffFirstErrorBlock   = 0x1A0 // __le64
	SbOffFirstErrorFunc    = 0x1A8 // [32]byte
	SbOffFirstErrorLine    = 0x1C8
	SbOffLastErrorTime     = 0x1CC
	SbOffLastErrorIno      = 0x1D0
	SbOffLastErrorLine     = 0x1D4
	SbOffLastErrorBlock    = 0x1D8 // __le64
	SbOffLastErrorFunc     = 0x1E0 // [32]byte
	SbOffMountOpts         = 0x200 // [64]byte
	SbOffUsrQuotaInum      = 0x240
	SbOffGrpQuotaInum      = 0x244
	SbOffOverheadClusters  = 0x248
	SbOffBackupBGs         = 0x24C // [2]__le32, meaningful with CompatSparseSuper2
	SbOffEncryptAlgos      = 0x254 // [4]__u8
	SbOffEncryptPwSalt     = 0x258 // [16]byte
	SbOffLpfIno            = 0x268
	SbOffPrjQuotaInum      = 0x26C
	SbOffChecksumSeed      = 0x270
	SbOffWriteTimeHi       = 0x274 // __u8
	SbOffMountTimeHi       = 0x275 // __u8
	SbOffMkfsTimeHi        = 0x276 // __u8
	SbOffLastCheckHi       = 0x277 // __u8
	SbOffFirstErrorTimeHi  = 0x278 // __u8
	SbOffLastErrorTimeHi   = 0x279 // __u8
	SbOffFirstErrorErrcode = 0x27A // __u8
	SbOffLastErrorErrcode  = 0x27B // __u8
	SbOffEncoding          = 0x27C // __le16
	SbOffEncodingFlags     = 0x27E // __le16
	SbOffOrphanFileInode   = 0x280
	SbOffReserved          = 0x284 // [94]__le32
	SbOffChecksum          = 0x3FC // also the number of bytes the checksum covers
)

// Superblock field widths, where a field is a fixed-size array rather than an
// integer.
const (
	UUIDLen        = 16
	VolumeNameLen  = 16
	LastMountedLen = 64
)

// Group descriptor field offsets. The first 32 bytes exist in every ext
// revision; the remainder only when the descriptor is 64 bytes wide. Fields are
// __le32 unless marked.
const (
	GdOffBlockBitmapLo   = 0x00
	GdOffInodeBitmapLo   = 0x04
	GdOffInodeTableLo    = 0x08
	GdOffFreeBlocksLo    = 0x0C // __le16
	GdOffFreeInodesLo    = 0x0E // __le16
	GdOffUsedDirsLo      = 0x10 // __le16
	GdOffFlags           = 0x12 // __le16
	GdOffExcludeBitmapLo = 0x14
	GdOffBlockBitmapCSLo = 0x18 // __le16
	GdOffInodeBitmapCSLo = 0x1A // __le16
	GdOffItableUnusedLo  = 0x1C // __le16
	GdOffChecksum        = 0x1E // __le16
	GdOffBlockBitmapHi   = 0x20
	GdOffInodeBitmapHi   = 0x24
	GdOffInodeTableHi    = 0x28
	GdOffFreeBlocksHi    = 0x2C // __le16
	GdOffFreeInodesHi    = 0x2E // __le16
	GdOffUsedDirsHi      = 0x30 // __le16
	GdOffItableUnusedHi  = 0x32 // __le16
	GdOffExcludeBitmapHi = 0x34
	GdOffBlockBitmapCSHi = 0x38 // __le16
	GdOffInodeBitmapCSHi = 0x3A // __le16
)

// Group descriptor sizes.
const (
	// GroupDescSizeMin is the descriptor size on a filesystem without the 64BIT
	// feature, and the minimum any filesystem may declare.
	GroupDescSizeMin = 32
	// GroupDescSize64Bit is the descriptor size once 64BIT is set, at which
	// point the high halves and the second checksum pair become meaningful.
	GroupDescSize64Bit = 64
)

// Inode field offsets: the whole of struct ext4_inode. Named because several
// of them are easy to transpose and the consequences are silent rather than
// loud. Fields are __le32 unless marked. Everything from InodeOffExtraISize on
// exists only in an inode larger than GoodOldInodeSize, and a field there is
// present only when i_extra_isize reaches past its end.
const (
	InodeOffMode        = 0x00 // __le16
	InodeOffUIDLo       = 0x02 // __le16
	InodeOffSizeLo      = 0x04
	InodeOffAtime       = 0x08
	InodeOffCtime       = 0x0C
	InodeOffMtime       = 0x10
	InodeOffDtime       = 0x14
	InodeOffGIDLo       = 0x18 // __le16
	InodeOffLinksCount  = 0x1A // __le16
	InodeOffBlocksLo    = 0x1C
	InodeOffFlags       = 0x20
	InodeOffVersion     = 0x24 // osd1.linux1.l_i_version
	InodeOffBlockRaw    = 0x28 // i_block, [BlockMapSize]byte
	InodeOffGeneration  = 0x64
	InodeOffFileACLLo   = 0x68
	InodeOffSizeHi      = 0x6C
	InodeOffObsoFaddr   = 0x70
	InodeOffBlocksHi    = 0x74 // __le16, osd2.linux2.l_i_blocks_hi
	InodeOffFileACLHi   = 0x76 // __le16, osd2.linux2.l_i_file_acl_high
	InodeOffUIDHi       = 0x78 // __le16
	InodeOffGIDHi       = 0x7A // __le16
	InodeOffChecksumLo  = 0x7C // __le16, osd2.linux2.l_i_checksum_lo
	InodeOffExtraISize  = 0x80 // __le16
	InodeOffChecksumHi  = 0x82 // __le16
	InodeOffCtimeExtra  = 0x84
	InodeOffMtimeExtra  = 0x88
	InodeOffAtimeExtra  = 0x8C
	InodeOffCrtime      = 0x90
	InodeOffCrtimeExtra = 0x94
	InodeOffVersionHi   = 0x98
	InodeOffProjectID   = 0x9C

	// inodeChecksumHiEnd is the first byte past i_checksum_hi. An inode carries
	// the high half only when its extra area extends at least this far.
	inodeChecksumHiEnd = 0x84
)

// Extent tree layout (fs/ext4/ext4_extents.h).
const (
	// ExtentMagic is eh_magic, carried by every node of an extent tree.
	ExtentMagic uint16 = 0xF30A
	// ExtentHeaderSize is the size of struct ext4_extent_header.
	ExtentHeaderSize = 12
	// ExtentEntrySize is the size of one struct ext4_extent or
	// struct ext4_extent_idx; the two share a slot size.
	ExtentEntrySize = 12
	// ExtentInitMaxLen is EXT_INIT_MAX_LEN. A leaf's ee_len up to and including
	// it is a written extent of that many blocks; a value above it is an
	// unwritten extent of ee_len - ExtentInitMaxLen blocks.
	ExtentInitMaxLen = 1 << 15
	// ExtentTailSize is the size of struct ext4_extent_tail, the checksum that
	// follows the last slot of a tree block - slot eh_max, not eh_entries.
	ExtentTailSize = 4
)

// Directory entry layout.
const (
	// DirEntryHeaderSize is inode(4) + rec_len(2) + name_len(1) + file_type(1).
	DirEntryHeaderSize = 8
	// MaxNameLen is the longest name a single record can hold.
	MaxNameLen = 255
	// DirTailSize is the size of struct ext4_dir_entry_tail, the fake record
	// that ends each directory block on a metadata_csum filesystem.
	DirTailSize = 12
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

	// maxWalkDepth bounds how deep a directory walk descends. The cycle guard
	// already stops a directory being entered twice, but a crafted image can
	// still chain hundreds of thousands of distinct directories, and every level
	// lengthens every path string beneath it. The cap is far past anything a real
	// filesystem produces.
	maxWalkDepth = 4096
)
