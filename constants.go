package libext

// On-disk constants.
//
// These are the values the ext2/3/4 format fixes, named as the kernel names
// them (fs/ext4/ext4.h) so that a reader and a writer of the format can share
// one table rather than each transcribing it. Every value here is read by this
// package or checked against the kernel's definition in its tests.
//
// Constants compared against a field of a parsed structure carry that field's
// type, so no conversion is needed: the feature bits are uint32 like
// Superblock.FeatureCompat, the mode bits uint16 like Inode.Mode, the inode
// flags uint32 like Inode.Flags, and the directory file types uint8 like
// DirEntry.FileType. Byte offsets and sizes are untyped.

const (
	// RootInode is the conventional root directory inode in EXT.
	RootInode uint32 = 2

	// BadBlocksInode holds the list of blocks marked unusable.
	BadBlocksInode uint32 = 1

	// GoodOldFirstIno is the first inode available to files on a revision 0
	// filesystem, and what mke2fs still sets s_first_ino to: 11, which is
	// conventionally lost+found.
	GoodOldFirstIno uint32 = 11

	// SuperblockOffset is the byte offset of the primary superblock.
	SuperblockOffset uint64 = 1024

	// SuperblockSize is the size of the superblock structure.
	SuperblockSize = 1024

	// Magic is s_magic, the superblock signature shared by ext2, ext3 and ext4.
	Magic uint16 = 0xEF53

	// GoodOldInodeSize is the size of a revision 0 inode, and of the fixed part
	// of every inode; anything beyond it exists only when i_extra_isize says so.
	GoodOldInodeSize = 128
)

// The block map: i_block, the 60 bytes of an inode that hold its extent tree
// root, its classic block pointers, or a short symlink target.
const (
	// NBlocks is EXT4_N_BLOCKS, the number of 32-bit words in i_block.
	NBlocks = 15

	// BlockMapSize is the size of i_block in bytes.
	BlockMapSize = NBlocks * 4

	// InlineSymlinkMax is the longest symlink target stored in i_block. The
	// kernel stores a target inline only when it fits with its terminating NUL
	// (fs/ext4/namei.c ext4_symlink), so it is one less than BlockMapSize: a
	// 60-byte target lives in a data block.
	InlineSymlinkMax = BlockMapSize - 1
)

// Compatible feature bits (s_feature_compat). A kernel that does not know one
// may still mount the filesystem read-write.
const (
	CompatDirPrealloc   uint32 = 0x0001
	CompatImagicInodes  uint32 = 0x0002
	CompatHasJournal    uint32 = 0x0004
	CompatExtAttr       uint32 = 0x0008
	CompatResizeInode   uint32 = 0x0010
	CompatDirIndex      uint32 = 0x0020
	CompatLazyBG        uint32 = 0x0040
	CompatExcludeInode  uint32 = 0x0080
	CompatExcludeBitmap uint32 = 0x0100
	CompatSparseSuper2  uint32 = 0x0200
	CompatFastCommit    uint32 = 0x0400
	CompatStableInodes  uint32 = 0x0800
	CompatOrphanFile    uint32 = 0x1000
)

// Incompatible feature bits (s_feature_incompat). A kernel that does not know
// one must refuse to mount the filesystem at all.
//
// IncompatInlineData and IncompatCasefold are read by Capabilities rather than
// by the parser: inline data is detected per inode from its flags (see
// InodeFlagInlineData), and casefolding changes only name comparison, which
// this read-only library never performs.
const (
	IncompatCompression uint32 = 0x0001
	IncompatFiletype    uint32 = 0x0002
	IncompatRecover     uint32 = 0x0004
	IncompatJournalDev  uint32 = 0x0008
	IncompatMetaBG      uint32 = 0x0010
	IncompatExtents     uint32 = 0x0040
	Incompat64Bit       uint32 = 0x0080
	IncompatMMP         uint32 = 0x0100
	IncompatFlexBG      uint32 = 0x0200
	IncompatEAInode     uint32 = 0x0400
	IncompatDirData     uint32 = 0x1000
	IncompatCSumSeed    uint32 = 0x2000
	IncompatLargedir    uint32 = 0x4000
	IncompatInlineData  uint32 = 0x8000
	IncompatEncrypt     uint32 = 0x10000
	IncompatCasefold    uint32 = 0x20000
)

// Read-only compatible feature bits (s_feature_ro_compat). A kernel that does
// not know one may mount the filesystem only read-only.
const (
	ROCompatSparseSuper   uint32 = 0x0001
	ROCompatLargeFile     uint32 = 0x0002
	ROCompatBtreeDir      uint32 = 0x0004
	ROCompatHugeFile      uint32 = 0x0008
	ROCompatGdtCsum       uint32 = 0x0010
	ROCompatDirNlink      uint32 = 0x0020
	ROCompatExtraIsize    uint32 = 0x0040
	ROCompatHasSnapshot   uint32 = 0x0080
	ROCompatQuota         uint32 = 0x0100
	ROCompatBigalloc      uint32 = 0x0200
	ROCompatMetadataCsum  uint32 = 0x0400
	ROCompatReplica       uint32 = 0x0800
	ROCompatReadonly      uint32 = 0x1000
	ROCompatProject       uint32 = 0x2000
	ROCompatSharedBlocks  uint32 = 0x4000
	ROCompatVerity        uint32 = 0x8000
	ROCompatOrphanPresent uint32 = 0x10000
)

// Inode flags (i_flags) that change how an inode is read.
const (
	// InodeFlagIndex marks a directory with an htree index.
	InodeFlagIndex uint32 = 0x00001000
	// InodeFlagHugeFile makes i_blocks count filesystem blocks instead of
	// 512-byte sectors.
	InodeFlagHugeFile uint32 = 0x00040000
	// InodeFlagExtents marks an inode whose i_block holds an extent tree.
	InodeFlagExtents uint32 = 0x00080000
	// InodeFlagEAInode marks an inode that stores a large extended attribute
	// value rather than a file.
	InodeFlagEAInode uint32 = 0x00200000
	// InodeFlagInlineData marks an inode whose data lives in the inode itself.
	InodeFlagInlineData uint32 = 0x10000000
)

// File type bits of i_mode.
const (
	ModeTypeMask uint16 = 0xF000
	ModeFIFO     uint16 = 0x1000
	ModeChar     uint16 = 0x2000
	ModeDir      uint16 = 0x4000
	ModeBlock    uint16 = 0x6000
	ModeRegular  uint16 = 0x8000
	ModeSymlink  uint16 = 0xA000
	ModeSocket   uint16 = 0xC000
)

// Directory entry file types (file_type, present with IncompatFiletype).
const (
	FileTypeUnknown uint8 = 0
	FileTypeRegular uint8 = 1
	FileTypeDir     uint8 = 2
	FileTypeChr     uint8 = 3
	FileTypeBlk     uint8 = 4
	FileTypeFIFO    uint8 = 5
	FileTypeSock    uint8 = 6
	FileTypeSymlink uint8 = 7

	// DirTailFileType marks the fake entry that ends a directory block on a
	// metadata_csum filesystem and holds the block's checksum.
	DirTailFileType uint8 = 0xDE
)

// Superblock field values.
const (
	// ChecksumTypeCRC32C is the only defined s_checksum_type.
	ChecksumTypeCRC32C uint8 = 1

	// Directory hash algorithms (s_def_hash_version). The unsigned variants
	// differ only in how bytes above 0x7F are widened.
	HashLegacy          uint8 = 0
	HashHalfMD4         uint8 = 1
	HashTEA             uint8 = 2
	HashLegacyUnsigned  uint8 = 3
	HashHalfMD4Unsigned uint8 = 4
	HashTEAUnsigned     uint8 = 5

	// FlagSignedHash and FlagUnsignedHash (s_flags) record which widening the
	// directory hash uses on this filesystem.
	FlagSignedHash   uint32 = 0x0001
	FlagUnsignedHash uint32 = 0x0002

	// Default mount options (s_default_mount_opts).
	DefMountUserXattr uint32 = 0x0004
	DefMountACL       uint32 = 0x0008
)
