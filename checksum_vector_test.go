package libext

import (
	"encoding/hex"
	"hash/crc32"
	"testing"
)

// The metadata checksum implementation was wrong in two compounding ways: it
// used hash/crc32, which applies a final complement that Linux's crc32c does
// not, and it hashed the whole superblock with the checksum field zeroed rather
// than the bytes preceding that field. Together they made every ext4 image with
// metadata_csum report mismatches, which made Options.VerifyChecksums unusable.
//
// These tests pin both properties against ground truth that does not come from
// this package.

// TestExtCRC32COmitsFinalComplement checks the primitive against the published
// CRC-32C check value for "123456789". The standard value is what hash/crc32
// produces; ext4's variant is its complement.
func TestExtCRC32COmitsFinalComplement(t *testing.T) {
	const input = "123456789"
	const standardCheckValue = 0xE3069283

	if got := crc32.Checksum([]byte(input), extCRC32CTable); got != standardCheckValue {
		t.Fatalf("CRC-32C table is wrong: Checksum = 0x%08x, want 0x%08x", got, standardCheckValue)
	}

	got := extCRC32C(crc32cInit, []byte(input))
	if want := uint32(^uint32(standardCheckValue)); got != want {
		t.Errorf("extCRC32C = 0x%08x, want 0x%08x (the standard value uncomplemented)", got, want)
	}
}

func TestExtCRC32CChainsLikeASingleCall(t *testing.T) {
	// Chaining is how ext4 builds every checksum: seed, then field, then field.
	data := []byte("the quick brown fox")

	whole := extCRC32C(crc32cInit, data)
	chained := extCRC32C(extCRC32C(crc32cInit, data[:7]), data[7:])

	if whole != chained {
		t.Errorf("chained = 0x%08x, single call = 0x%08x", chained, whole)
	}
}

// TestGroupDescriptorChecksumAgainstRealImage uses a descriptor taken verbatim
// from an image built by mke2fs 1.47 with -t ext4 -b 1024 -I 256 and a fixed
// UUID. The stored checksum is mke2fs's own, so agreeing with it is agreement
// with the reference implementation.
func TestGroupDescriptorChecksumAgainstRealImage(t *testing.T) {
	const (
		uuidHex = "11111111222233334444555555555555"
		descHex = "820000008400000086000000e019ef070300000000000000" +
			"f336c1b5ef07fdb100000000000000000000000000000000" +
			"0000000000000000fd913bbd00000000"
	)

	uuid, err := hex.DecodeString(uuidHex)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := hex.DecodeString(descHex)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 64 {
		t.Fatalf("descriptor vector is %d bytes, want 64", len(desc))
	}

	fs := &FS{sb: Superblock{
		FeatureROCompat: featureRoCompatMetadataCS,
		GroupDescSize:   64,
	}}
	copy(fs.sb.UUID[:], uuid)

	if err := fs.verifyGroupDescriptorChecksum(0, desc); err != nil {
		t.Errorf("real group descriptor failed verification: %v", err)
	}

	// A single flipped bit must be caught.
	tampered := make([]byte, len(desc))
	copy(tampered, desc)
	tampered[0] ^= 0x01
	if err := fs.verifyGroupDescriptorChecksum(0, tampered); err == nil {
		t.Error("tampered group descriptor passed verification")
	}

	// The checksum is bound to the group number, so the same bytes must fail
	// for a different group.
	if err := fs.verifyGroupDescriptorChecksum(1, desc); err == nil {
		t.Error("descriptor verified against the wrong group number")
	}
}

// TestSuperblockChecksumCoversOnlyPrecedingBytes builds a superblock, stamps the
// correct checksum, and confirms verification accepts it — then confirms that
// including the trailing bytes, as the old code did, would not.
func TestSuperblockChecksumCoversOnlyPrecedingBytes(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.roCompat = featureRoCompatMetadataCS
	img := buildTestImage(t, cfg)
	sb := img[superblockOffset : superblockOffset+superblockSize]

	// Stamp the checksum the way mke2fs does: over the bytes preceding the field.
	correct := extCRC32C(crc32cInit, sb[:superblockChecksumOffset])
	sb[superblockChecksumOffset+0] = byte(correct)
	sb[superblockChecksumOffset+1] = byte(correct >> 8)
	sb[superblockChecksumOffset+2] = byte(correct >> 16)
	sb[superblockChecksumOffset+3] = byte(correct >> 24)

	fs := &FS{sb: Superblock{FeatureROCompat: featureRoCompatMetadataCS}}
	if err := fs.verifySuperblockChecksum(sb); err != nil {
		t.Errorf("correctly stamped superblock failed verification: %v", err)
	}

	// The old computation folded in the four zeroed checksum bytes.
	padded := make([]byte, superblockSize)
	copy(padded, sb)
	for i := superblockChecksumOffset; i < superblockSize; i++ {
		padded[i] = 0
	}
	if extCRC32C(crc32cInit, padded) == correct {
		t.Error("hashing the padded superblock gives the same value; the test cannot detect the regression")
	}

	// And a tampered superblock must fail.
	sb[0] ^= 0x01
	if err := fs.verifySuperblockChecksum(sb); err == nil {
		t.Error("tampered superblock passed verification")
	}
}

// TestChecksumSeedIsHonoured pins the CSUM_SEED path: when the filesystem
// stores a seed, checksums must be keyed on it rather than on the volume UUID.
func TestChecksumSeedIsHonoured(t *testing.T) {
	fs := &FS{sb: Superblock{
		FeatureROCompat: featureRoCompatMetadataCS,
		FeatureIncompat: featureIncompatCSumSeed,
		ChecksumSeed:    0x806ce908,
	}}
	copy(fs.sb.UUID[:], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	if got := fs.csumSeed(); got != 0x806ce908 {
		t.Errorf("csumSeed = 0x%08x, want the stored seed 0x806ce908", got)
	}

	// Without the feature, the seed is derived from the UUID.
	fs.sb.FeatureIncompat = 0
	if got := fs.csumSeed(); got != extCRC32C(crc32cInit, fs.sb.UUID[:]) {
		t.Errorf("csumSeed = 0x%08x, want the UUID-derived seed", got)
	}
}

// TestVerifyChecksumsOptionAcceptsAValidImage guards the option end to end: it
// was unusable, because a correct image always failed to open.
func TestVerifyChecksumsOptionAcceptsAValidImage(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.roCompat = featureRoCompatMetadataCS
	img := buildTestImage(t, cfg)
	sb := img[superblockOffset : superblockOffset+superblockSize]

	correct := extCRC32C(crc32cInit, sb[:superblockChecksumOffset])
	sb[superblockChecksumOffset+0] = byte(correct)
	sb[superblockChecksumOffset+1] = byte(correct >> 8)
	sb[superblockChecksumOffset+2] = byte(correct >> 16)
	sb[superblockChecksumOffset+3] = byte(correct >> 24)

	// Stamp the group descriptor too.
	gd := img[testGroupDescOffset : testGroupDescOffset+32]
	gd[0x1E], gd[0x1F] = 0, 0
	g := []byte{0, 0, 0, 0}
	seed := extCRC32C(crc32cInit, make([]byte, 16)) // UUID is all zeros in the fixture
	crc := extCRC32C(extCRC32C(seed, g), gd)
	gd[0x1E] = byte(crc)
	gd[0x1F] = byte(crc >> 8)

	if _, err := OpenWithOptions(newFixtureReader(img), Options{VerifyChecksums: true}); err != nil {
		t.Fatalf("a correctly checksummed image failed to open with VerifyChecksums: %v", err)
	}
}
