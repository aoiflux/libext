package libext

import "time"

const (
	superblockOffset = 1024
	superblockSize   = 1024
	extMagic         = 0xEF53

	// maxBlockGroups bounds the group count derived from superblock geometry.
	// 2^24 groups is roughly 2 PB at a 4 KiB block size with 32768 blocks per
	// group; anything larger indicates a corrupt or crafted superblock.
	maxBlockGroups = 1 << 24
)

type FSKind string

const (
	FSKindUnknown FSKind = "unknown"
	FSKindExt1    FSKind = "ext1"
	FSKindExt2    FSKind = "ext2"
	FSKindExt3    FSKind = "ext3"
	FSKindExt4    FSKind = "ext4"
)

const (
	inodeModeTypeMask = 0xF000
	inodeTypeFIFO     = 0x1000
	inodeTypeChar     = 0x2000
	inodeTypeDir      = 0x4000
	inodeTypeBlock    = 0x6000
	inodeTypeRegular  = 0x8000
	inodeTypeSymlink  = 0xA000
	inodeTypeSocket   = 0xC000
)

const (
	inodeFlagExtents = 0x00080000
)

const (
	featureCompatHasJournal = 0x0004
	featureCompatDirIndex   = 0x0020

	featureIncompatFileType = 0x0002
	featureIncompatExtents  = 0x0040
	featureIncompat64Bit    = 0x0080
	featureIncompatCSumSeed = 0x2000

	featureRoCompatSparseSuper = 0x0001
	featureRoCompatGDTChecksum = 0x0010
	featureRoCompatMetadataCS  = 0x0400
)

// Superblock contains the subset of EXT superblock fields needed by this parser.
type Superblock struct {
	InodesCount       uint32
	BlocksCount       uint64
	ReservedBlocks    uint64
	FreeBlocks        uint64
	FreeInodes        uint32
	FirstDataBlock    uint32
	LogBlockSize      uint32
	BlockSize         uint32
	BlocksPerGroup    uint32
	InodesPerGroup    uint32
	Magic             uint16
	RevisionLevel     uint32
	InodeSize         uint16
	FirstInode        uint32
	FeatureCompat     uint32
	FeatureIncompat   uint32
	FeatureROCompat   uint32
	DescSize          uint16
	UUID              [16]byte
	VolumeName        string
	LastMounted       string
	MountTime         time.Time
	WriteTime         time.Time
	MountCount        uint16
	MaxMountCount     uint16
	State             uint16
	ErrorsBehavior    uint16
	CreatorOS         uint32
	JournalInode      uint32
	JournalDevice     uint32
	LastOrphan        uint32
	GroupsCount       uint32
	GroupDescSize     uint16
	GroupDescTableOff uint64
}

// GroupDescriptor represents one block group descriptor.
type GroupDescriptor struct {
	Group            uint32
	BlockBitmapBlock uint64
	InodeBitmapBlock uint64
	InodeTableBlock  uint64
	FreeBlocksCount  uint32
	FreeInodesCount  uint32
	UsedDirsCount    uint32
	Flags            uint16
}

// Inode is a normalized inode view.
type Inode struct {
	Number      uint32
	Mode        uint16
	UID         uint32
	GID         uint32
	Size        uint64
	Atime       time.Time
	Ctime       time.Time
	Mtime       time.Time
	Dtime       time.Time
	LinksCount  uint16
	Blocks512   uint64
	Flags       uint32
	Generation  uint32
	FileACL     uint64
	BlockRaw    [60]byte
	HasExtents  bool
	IsDirectory bool
	IsRegular   bool
	IsSymlink   bool
}

// DirEntry is a parsed directory entry.
type DirEntry struct {
	Name        string
	Inode       uint32
	RecLen      uint16
	NameLen     uint8
	FileType    uint8
	IsDirectory bool
	Size        uint64
}
