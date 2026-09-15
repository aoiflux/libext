package libext

import (
	"encoding/binary"
	"encoding/json"
	"regexp"
	"testing"
)

// Capabilities is answered from the superblock, so the test that matters is
// that two volumes disagree. A hardcoded field set would pass every assertion
// about ext4 and still be wrong, because the question this type exists to
// answer - "does this filesystem record X" - has a different answer on ext2.

// ext2SBConfig is the default fixture: 128-byte inodes, no feature flags beyond
// the file-type bit, and therefore no journal, no extents and no room in the
// inode for a birth time.
func ext2SBConfig() sbConfig {
	return defaultSBConfig()
}

// ext4SBConfig turns on the features whose presence Capabilities reports, so
// every volume-state field lands on the opposite value from the ext2 fixture.
func ext4SBConfig() sbConfig {
	cfg := defaultSBConfig()
	cfg.inodeSize = 256
	cfg.compat = featureCompatHasJournal | featureCompatDirIndex | featureCompatExtAttr
	cfg.incompat = featureIncompatFileType | featureIncompatExtents |
		featureIncompat64Bit | featureIncompatInlineData
	// BIGALLOC is deliberately absent: Open refuses it outright, so a fixture
	// carrying it could not be opened at all. TestCapabilitiesBigallocNeedsPermissive
	// covers that field on its own.
	cfg.roCompat = featureRoCompatMetadataCS
	return cfg
}

// buildExt4Image renders the ext4 fixture and gives it an internal journal.
// buildTestImage leaves s_journal_inum zero, which Capabilities reads as a
// journal living on some other device.
func buildExt4Image(t testing.TB) []byte {
	t.Helper()
	img := buildTestImage(t, ext4SBConfig())
	binary.LittleEndian.PutUint32(img[superblockOffset+sbOffJournalInode:], 8)
	return img
}

func TestCapabilitiesReflectTheVolumeNotTheFormat(t *testing.T) {
	ext2 := openFixture(t, buildTestImage(t, ext2SBConfig()), Options{}).Capabilities()
	ext4 := openFixture(t, buildExt4Image(t), Options{}).Capabilities()

	// Each pair is a field that must differ. If any of these compare equal the
	// value is being invented rather than read.
	volumeState := []struct {
		field      string
		got2, got4 bool
	}{
		{"Journaled", ext2.Journaled, ext4.Journaled},
		{"CreationTimes", ext2.CreationTimes, ext4.CreationTimes},
		{"SubSecondTimestamps", ext2.SubSecondTimestamps, ext4.SubSecondTimestamps},
		{"ExtendedAttributes", ext2.ExtendedAttributes, ext4.ExtendedAttributes},
		{"Extents", ext2.Extents, ext4.Extents},
		{"SixtyFourBit", ext2.SixtyFourBit, ext4.SixtyFourBit},
		{"DirectoryIndex", ext2.DirectoryIndex, ext4.DirectoryIndex},
		{"InlineData", ext2.InlineData, ext4.InlineData},
		{"MetadataChecksums", ext2.MetadataChecksums, ext4.MetadataChecksums},
	}
	for _, c := range volumeState {
		if c.got2 {
			t.Errorf("%s = true on the ext2 fixture, want false", c.field)
		}
		if !c.got4 {
			t.Errorf("%s = false on the ext4 fixture, want true", c.field)
		}
	}

	// The format constants must NOT vary, for the same reason: a field that
	// tracks the volume when it describes ext itself is equally wrong.
	if ext2.POSIXPermissions != ext4.POSIXPermissions ||
		ext2.HardLinks != ext4.HardLinks ||
		ext2.SymbolicLinks != ext4.SymbolicLinks ||
		ext2.SparseFiles != ext4.SparseFiles ||
		ext2.AccessTimes != ext4.AccessTimes ||
		ext2.ModificationTimes != ext4.ModificationTimes ||
		ext2.MetadataChangeTimes != ext4.MetadataChangeTimes {
		t.Error("a format-constant field differs between the two volumes")
	}
	if !ext2.POSIXPermissions || !ext2.HardLinks || !ext2.SymbolicLinks ||
		!ext2.SparseFiles || !ext2.StableFileIdentity || !ext2.IdentityReuseCounter {
		t.Errorf("format constants are not all set: %+v", ext2)
	}
	if ext2.Compression || ext2.TimezoneOffsets || ext2.UnicodeNames {
		t.Errorf("a capability ext does not have is reported: %+v", ext2)
	}
	if !ext2.CaseSensitive || !ext4.CaseSensitive {
		t.Error("CaseSensitive is false without the CASEFOLD feature")
	}
}

func TestCapabilitiesJournalOnAnotherDeviceIsNotReadable(t *testing.T) {
	// HAS_JOURNAL with no journal inode is what an external journal device
	// looks like from here: the volume has a journal, but not one this library
	// can reach, and reporting true would promise transactions it cannot read.
	cfg := defaultSBConfig()
	cfg.compat = featureCompatHasJournal
	fs := openFixture(t, buildTestImage(t, cfg), Options{})

	if fs.Capabilities().Journaled {
		t.Error("Journaled = true for a volume whose journal inode is zero")
	}
}

func TestCapabilitiesCaseFoldingVolumeIsNotCaseSensitive(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.incompat = featureIncompatFileType | featureIncompatCasefold
	fs := openFixture(t, buildTestImage(t, cfg), Options{})

	if fs.Capabilities().CaseSensitive {
		t.Error("CaseSensitive = true on a volume carrying CASEFOLD")
	}
}

func TestCapabilitiesBigallocNeedsPermissive(t *testing.T) {
	// A bigalloc volume opens only permissively, and the offsets it then
	// reports are cluster-based rather than block-based. Capabilities is where
	// a caller learns that, so the field has to survive the permissive path.
	cfg := defaultSBConfig()
	cfg.roCompat = featureRoCompatBigalloc
	img := buildTestImage(t, cfg)

	if _, err := OpenWithOptions(newFixtureReader(img), Options{}); err == nil {
		t.Fatal("Open accepted a BIGALLOC volume; the fixture no longer tests anything")
	}

	fs := openFixture(t, img, Options{Permissive: true})
	if !fs.Capabilities().Bigalloc {
		t.Error("Bigalloc = false on a volume carrying the BIGALLOC feature")
	}
}

func TestCapabilitiesNilFS(t *testing.T) {
	var fs *FS
	if got := fs.Capabilities(); got != (Capabilities{}) {
		t.Errorf("nil FS reported %+v, want the zero value", got)
	}
}

// camelCaseKey is the shape this family has standardised against: five of the
// six libraries tag exclusively in snake_case, and this test keeps libext from
// drifting the way a new field easily could.
var camelCaseKey = regexp.MustCompile(`"[a-z0-9_]*[a-z0-9][A-Z]`)

func TestCapabilitiesJSONTagsAreSnakeCase(t *testing.T) {
	fs := openFixture(t, buildExt4Image(t), Options{})

	buf, err := json.Marshal(fs.Capabilities())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if camelCaseKey.Match(buf) {
		t.Errorf("Capabilities JSON has a camelCase key: %s", buf)
	}
	// A struct with no tags at all would marshal as Go field names, which are
	// camelCase-free only by accident of starting capitalised. Check one key
	// explicitly so the regex cannot pass vacuously.
	var keys map[string]any
	if err := json.Unmarshal(buf, &keys); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := keys["sub_second_timestamps"]; !ok {
		t.Errorf("no sub_second_timestamps key; tags are missing: %s", buf)
	}
}
