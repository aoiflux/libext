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

	// Blocking marks a feature that changes how on-disk structures are
	// addressed, such that parsing an image without honouring it yields
	// confidently wrong offsets rather than missing data. Blocking features
	// prevent Open from succeeding unless Options.Permissive is set.
	//
	// An unsupported incompat feature is implicitly blocking; this field exists
	// for features in the other two sets that are equally unsafe to ignore.
	Blocking bool
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
	{
		Name:        "STABLE_INODES",
		Description: "Inode numbers are stable across resize",
		FlagType:    "compat",
		FlagValue:   0x0800,
		Status:      FeatureStatusSupported,
	},
	{
		Name:        "ORPHAN_FILE",
		Description: "Orphan list stored in a dedicated file",
		FlagType:    "compat",
		FlagValue:   0x1000,
		Status:      FeatureStatusPartial,
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
		// The group descriptor table is not contiguous under META_BG. Parsing it
		// as if it were yields wrong inode table offsets for every group, which
		// is worse than refusing the image.
		Name:        "META_BG",
		Description: "Meta block group (non-contiguous group descriptor table)",
		FlagType:    "incompat",
		FlagValue:   0x0010,
		Status:      FeatureStatusUnsupported,
		Blocking:    true,
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
		// 0x0400 is EA_INODE, not LARGEDIR. Extended attribute values may live in
		// their own inode; the block-resident case is handled, the e_value_inum
		// case is not yet followed.
		Name:        "EA_INODE",
		Description: "Extended attribute values stored in their own inode",
		FlagType:    "incompat",
		FlagValue:   0x0400,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "DIRDATA",
		Description: "Data stored in directory entries",
		FlagType:    "incompat",
		FlagValue:   0x1000,
		Status:      FeatureStatusUnsupported,
	},
	{
		// Metadata checksums are seeded from s_checksum_seed rather than the
		// volume UUID when this is set. Reads are unaffected; only checksum
		// verification is, so this is not blocking.
		Name:        "CSUM_SEED",
		Description: "Metadata checksum seed stored in the superblock",
		FlagType:    "incompat",
		FlagValue:   0x2000,
		Status:      FeatureStatusPartial,
	},
	{
		Name:        "LARGEDIR",
		Description: "Large directory support (>2GB, 3-level HTree)",
		FlagType:    "incompat",
		FlagValue:   0x4000,
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
	{
		// Casefolding changes name comparison, not name storage: entries are read
		// correctly, but case-insensitive lookup is not implemented.
		Name:        "CASEFOLD",
		Description: "Case-insensitive filename lookup",
		FlagType:    "incompat",
		FlagValue:   0x20000,
		Status:      FeatureStatusPartial,
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
		// Under BIGALLOC extents address clusters, not blocks. Every physical
		// offset this library reports would be scaled by the cluster factor, so
		// the image is refused rather than answered wrongly.
		Name:        "BIGALLOC",
		Description: "Big allocation clusters (extents address clusters, not blocks)",
		FlagType:    "ro_compat",
		FlagValue:   0x0200,
		Status:      FeatureStatusUnsupported,
		Blocking:    true,
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
	{
		Name:        "ORPHAN_PRESENT",
		Description: "Orphan file contains entries needing recovery",
		FlagType:    "ro_compat",
		FlagValue:   0x10000,
		Status:      FeatureStatusPartial,
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

// flagsFor returns the enabled bitmask for one feature set.
func (fs *FS) flagsFor(flagType string) uint32 {
	switch flagType {
	case "compat":
		return fs.sb.FeatureCompat
	case "incompat":
		return fs.sb.FeatureIncompat
	case "ro_compat":
		return fs.sb.FeatureROCompat
	default:
		return 0
	}
}

// knownFeatureMask returns every bit AllFeatures describes for a feature set.
func knownFeatureMask(flagType string) uint32 {
	var mask uint32
	for _, f := range AllFeatures {
		if f.FlagType == flagType {
			mask |= f.FlagValue
		}
	}
	return mask
}

// UnknownFeatureBits returns the enabled bits in a feature set that this version
// does not describe. An unknown incompat bit means the on-disk layout may differ
// in ways the parser cannot detect.
func (fs *FS) UnknownFeatureBits(flagType string) uint32 {
	return fs.flagsFor(flagType) &^ knownFeatureMask(flagType)
}

// BlockingFeatures returns the enabled features that prevent correct parsing:
// unsupported incompat features, and features in any set marked Blocking.
func (fs *FS) BlockingFeatures() []Feature {
	var blocking []Feature
	for _, f := range AllFeatures {
		if (fs.flagsFor(f.FlagType) & f.FlagValue) == 0 {
			continue
		}
		if f.Blocking || (f.FlagType == "incompat" && f.Status == FeatureStatusUnsupported) {
			blocking = append(blocking, f)
		}
	}
	return blocking
}

// CheckRequiredFeatures validates that required features are supported.
//
// It returns an error when an unsupported incompat feature is set, when a
// feature marked Blocking is set, or when an incompat bit this version does not
// recognise is set. The last case matters most: an unrecognised incompat bit
// means the layout may differ in ways the parser cannot detect, so answering at
// all would be answering wrongly.
func (fs *FS) CheckRequiredFeatures() error {
	var reasons []string
	for _, f := range fs.BlockingFeatures() {
		reasons = append(reasons, fmt.Sprintf("%s:%s (%s)", f.FlagType, f.Name, f.Description))
	}
	if bits := fs.UnknownFeatureBits("incompat"); bits != 0 {
		reasons = append(reasons, fmt.Sprintf("incompat: unrecognised feature bits 0x%08x", bits))
	}
	if len(reasons) == 0 {
		return nil
	}

	msg := "unsupported ext features (set Options.Permissive to parse anyway):"
	for _, r := range reasons {
		msg += "\n  - " + r
	}
	return fmt.Errorf("%s", msg)
}

// checkFeatures runs the open-time feature gate and records what was tolerated.
func (fs *FS) checkFeatures() error {
	for _, flagType := range []string{"compat", "incompat", "ro_compat"} {
		if bits := fs.UnknownFeatureBits(flagType); bits != 0 {
			fs.warn(WarnUnknownFeature, flagType, fmt.Sprintf("unrecognised feature bits 0x%08x", bits))
		}
	}
	for _, fname := range findUnsupportedFeatures(fs.sb.FeatureROCompat, "ro_compat") {
		fs.warn(WarnUnsupportedFeature, "ro_compat:"+fname, "feature is not interpreted")
	}
	for _, fname := range findUnsupportedFeatures(fs.sb.FeatureCompat, "compat") {
		fs.warn(WarnUnsupportedFeature, "compat:"+fname, "feature is not interpreted")
	}
	if (fs.sb.FeatureIncompat & featureIncompatCSumSeed) != 0 {
		fs.warn(WarnChecksumMismatch, "incompat:CSUM_SEED",
			"checksums are seeded from s_checksum_seed; verification against the volume UUID may report false mismatches")
	}

	if err := fs.CheckRequiredFeatures(); err != nil {
		if !fs.opts.Permissive {
			return err
		}
		fs.warn(WarnUnsupportedFeature, "", err.Error())
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
