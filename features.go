package libext

import (
	"fmt"
)

// FeatureStatus represents the implementation status of an ext feature.
type FeatureStatus int

const (
	FeatureStatusSupported FeatureStatus = iota
	FeatureStatusPartial
	FeatureStatusUnsupported
)

// Feature represents an ext filesystem feature with metadata.
type Feature struct {
	Name        string
	Description string
	FlagType    string // "compat", "incompat", or "ro_compat"
	FlagValue   uint32
	Status      FeatureStatus
}

// AllFeatures defines all known ext filesystem features.
var AllFeatures = []Feature{
	// compat features (can safely ignore if not recognized)
	{
		Name:        "DIR_PREALLOC",
		Description: "Directory pre-allocation",
		FlagType:    "compat",
		FlagValue:   0x0001,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "IMAGIC_INODES",
		Description: "IMAGIC inodes",
		FlagType:    "compat",
		FlagValue:   0x0002,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "HAS_JOURNAL",
		Description: "Has a journal (ext3/ext4)",
		FlagType:    "compat",
		FlagValue:   0x0004,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "EXT_ATTR",
		Description: "Extended attributes",
		FlagType:    "compat",
		FlagValue:   0x0008,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "RESIZE_INODE",
		Description: "Resize inode",
		FlagType:    "compat",
		FlagValue:   0x0010,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "DIR_INDEX",
		Description: "Directory indexing (HTree)",
		FlagType:    "compat",
		FlagValue:   0x0020,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "LAZY_BG",
		Description: "Lazy block group initialization",
		FlagType:    "compat",
		FlagValue:   0x0040,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "EXCLUDE_INODE",
		Description: "Exclude inode",
		FlagType:    "compat",
		FlagValue:   0x0080,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "EXCLUDE_BITMAP",
		Description: "Exclude bitmap",
		FlagType:    "compat",
		FlagValue:   0x0100,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "SPARSE_SUPER2",
		Description: "Sparse superblock version 2",
		FlagType:    "compat",
		FlagValue:   0x0200,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "FAST_COMMIT",
		Description: "Fast commit",
		FlagType:    "compat",
		FlagValue:   0x0400,
		Status:      FeatureStatusUnsupported,
	},

	// incompat features (must understand to read/write filesystem)
	{
		Name:        "COMPRESSION",
		Description: "Compression",
		FlagType:    "incompat",
		FlagValue:   0x0001,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "FILETYPE",
		Description: "File type in directory entries",
		FlagType:    "incompat",
		FlagValue:   0x0002,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "RECOVER",
		Description: "Filesystem needs recovery",
		FlagType:    "incompat",
		FlagValue:   0x0004,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "JOURNAL_DEV",
		Description: "Journal device",
		FlagType:    "incompat",
		FlagValue:   0x0008,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "META_BG",
		Description: "Meta block group",
		FlagType:    "incompat",
		FlagValue:   0x0010,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "EXTENTS",
		Description: "Extent-based allocation",
		FlagType:    "incompat",
		FlagValue:   0x0040,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "64BIT",
		Description: "64-bit block/inode support",
		FlagType:    "incompat",
		FlagValue:   0x0080,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "MMP",
		Description: "Multiple mount protection",
		FlagType:    "incompat",
		FlagValue:   0x0100,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "FLEX_BG",
		Description: "Flexible block group",
		FlagType:    "incompat",
		FlagValue:   0x0200,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "LARGEDIR",
		Description: "Large directory support",
		FlagType:    "incompat",
		FlagValue:   0x0400,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "INLINE_DATA",
		Description: "Inline data in inode",
		FlagType:    "incompat",
		FlagValue:   0x8000,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "ENCRYPT",
		Description: "Filesystem encryption",
		FlagType:    "incompat",
		FlagValue:   0x10000,
		Status:      FeatureStatusUnsupported,
	},

	// ro_compat features (can safely ignore if not recognized, but must be careful)
	{
		Name:        "SPARSE_SUPER",
		Description: "Sparse superblock",
		FlagType:    "ro_compat",
		FlagValue:   0x0001,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "LARGE_FILE",
		Description: "Large file support (>2GB)",
		FlagType:    "ro_compat",
		FlagValue:   0x0002,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "BTREE_DIR",
		Description: "Binary tree directory (deprecated)",
		FlagType:    "ro_compat",
		FlagValue:   0x0004,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "HUGE_FILE",
		Description: "Huge file support",
		FlagType:    "ro_compat",
		FlagValue:   0x0008,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "GDT_CSUM",
		Description: "Group descriptor checksum",
		FlagType:    "ro_compat",
		FlagValue:   0x0010,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "DIR_NLINK",
		Description: "Directory nlink",
		FlagType:    "ro_compat",
		FlagValue:   0x0020,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "EXTRA_ISIZE",
		Description: "Extra inode size",
		FlagType:    "ro_compat",
		FlagValue:   0x0040,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "HAS_SNAPSHOT",
		Description: "Has snapshot",
		FlagType:    "ro_compat",
		FlagValue:   0x0080,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "QUOTA",
		Description: "Quota",
		FlagType:    "ro_compat",
		FlagValue:   0x0100,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "BIGALLOC",
		Description: "Big allocation clusters",
		FlagType:    "ro_compat",
		FlagValue:   0x0200,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "METADATA_CSUM",
		Description: "Metadata checksums (CRC32C)",
		FlagType:    "ro_compat",
		FlagValue:   0x0400,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "REPLICA",
		Description: "Replica",
		FlagType:    "ro_compat",
		FlagValue:   0x0800,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "READONLY",
		Description: "Read-only",
		FlagType:    "ro_compat",
		FlagValue:   0x1000,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "PROJECT",
		Description: "Project quota",
		FlagType:    "ro_compat",
		FlagValue:   0x2000,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "SHARED_BLOCKS",
		Description: "Shared blocks",
		FlagType:    "ro_compat",
		FlagValue:   0x4000,
		Status:      FeatureStatusUnsupported,
	},
	{
		Name:        "VERITY",
		Description: "Verity checksums",
		FlagType:    "ro_compat",
		FlagValue:   0x8000,
		Status:      FeatureStatusUnsupported,
	},
}

// GetFeatureStatus returns the status and description of a feature.
func GetFeatureStatus(flagType string, flagValue uint32) (FeatureStatus, string) {
	for _, f := range AllFeatures {
		if f.FlagType == flagType && f.FlagValue == flagValue {
			return f.Status, f.Description
		}
	}
	return FeatureStatusUnsupported, "unknown feature"
}

// CheckRequiredFeatures validates that required features are supported.
// Returns an error if an unsupported incompat feature is set.
func (fs *FS) CheckRequiredFeatures() error {
	// Check incompat features (must be understood)
	unsupported := findUnsupportedFeatures(fs.sb.FeatureIncompat, "incompat")
	if len(unsupported) > 0 {
		msg := "unsupported incompatible features:"
		for _, fname := range unsupported {
			msg += "\n  - " + fname
		}
		return fmt.Errorf("%s", msg)
	}

	return nil
}

// CheckOptionalFeatures warns about partially supported features.
// Returns a list of partial features but does not fail.
func (fs *FS) CheckOptionalFeatures() []string {
	var partial []string

	// Check ro_compat features
	for _, fname := range findPartialFeatures(fs.sb.FeatureROCompat, "ro_compat") {
		partial = append(partial, fmt.Sprintf("ro_compat:%s", fname))
	}

	// Check compat features (can be ignored)
	for _, fname := range findPartialFeatures(fs.sb.FeatureCompat, "compat") {
		partial = append(partial, fmt.Sprintf("compat:%s", fname))
	}

	// Check incompat features (these are partial support, not errors)
	for _, fname := range findPartialFeatures(fs.sb.FeatureIncompat, "incompat") {
		partial = append(partial, fmt.Sprintf("incompat:%s", fname))
	}

	return partial
}

// findUnsupportedFeatures finds all unsupported features in a feature set.
func findUnsupportedFeatures(flags uint32, flagType string) []string {
	var unsupported []string

	for _, f := range AllFeatures {
		if f.FlagType != flagType {
			continue
		}
		if (flags & f.FlagValue) == 0 {
			continue
		}
		if f.Status == FeatureStatusUnsupported {
			unsupported = append(unsupported, f.Name)
		}
	}

	return unsupported
}

// findPartialFeatures finds all partially supported features in a feature set.
func findPartialFeatures(flags uint32, flagType string) []string {
	var partial []string

	for _, f := range AllFeatures {
		if f.FlagType != flagType {
			continue
		}
		if (flags & f.FlagValue) == 0 {
			continue
		}
		if f.Status == FeatureStatusPartial {
			partial = append(partial, f.Name)
		}
	}

	return partial
}

// DescribeFeatures returns a human-readable description of enabled features.
func (fs *FS) DescribeFeatures() string {
	var desc string

	desc += "Enabled features:\n"

	if fs.sb.FeatureCompat != 0 {
		desc += "  compat (optional):\n"
		for _, f := range AllFeatures {
			if f.FlagType == "compat" && (fs.sb.FeatureCompat&f.FlagValue) != 0 {
				desc += fmt.Sprintf("    - %s (%s)\n", f.Name, featureStatusString(f.Status))
			}
		}
	}

	if fs.sb.FeatureIncompat != 0 {
		desc += "  incompat (required to understand):\n"
		for _, f := range AllFeatures {
			if f.FlagType == "incompat" && (fs.sb.FeatureIncompat&f.FlagValue) != 0 {
				desc += fmt.Sprintf("    - %s (%s)\n", f.Name, featureStatusString(f.Status))
			}
		}
	}

	if fs.sb.FeatureROCompat != 0 {
		desc += "  ro_compat (read-only safe):\n"
		for _, f := range AllFeatures {
			if f.FlagType == "ro_compat" && (fs.sb.FeatureROCompat&f.FlagValue) != 0 {
				desc += fmt.Sprintf("    - %s (%s)\n", f.Name, featureStatusString(f.Status))
			}
		}
	}

	return desc
}

func featureStatusString(status FeatureStatus) string {
	switch status {
	case FeatureStatusSupported:
		return "supported"
	case FeatureStatusPartial:
		return "partial"
	case FeatureStatusUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}
