package libext

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// timestamps
// ---------------------------------------------------------------------------

func TestTimeRangeBounds(t *testing.T) {
	if got := MinTime.Format(time.RFC3339Nano); got != "1901-12-13T20:45:52Z" {
		t.Errorf("MinTime = %s", got)
	}
	if got := MaxTime.Format(time.RFC3339Nano); got != "2446-05-10T22:38:55.999999999Z" {
		t.Errorf("MaxTime = %s", got)
	}
}

func TestEncodeTimeMatchesKernel(t *testing.T) {
	for _, tc := range kernelTimeCases {
		at, err := time.Parse(time.RFC3339, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		at = at.Add(987654321)
		wantSec, wantExtra := kernelEncodeTime(at.Unix(), int64(at.Nanosecond()))
		sec, extra := EncodeTime(at)
		if sec != wantSec || extra != wantExtra {
			t.Errorf("%s: EncodeTime = (0x%08x, 0x%08x), kernel (0x%08x, 0x%08x)",
				tc.name, sec, extra, wantSec, wantExtra)
		}
	}
}

// roundTrip encodes and decodes t, failing on any change, and checks the
// encoding against the kernel's rule as written out in kernelEncodeTime.
func roundTrip(t *testing.T, at time.Time) {
	t.Helper()
	sec, extra := EncodeTime(at)
	if ks, ke := kernelEncodeTime(at.Unix(), int64(at.Nanosecond())); sec != ks || extra != ke {
		t.Fatalf("%s: EncodeTime = (0x%08x, 0x%08x), kernel (0x%08x, 0x%08x)",
			at.Format(time.RFC3339Nano), sec, extra, ks, ke)
	}
	if got := DecodeTime(sec, extra); !got.Equal(at) {
		t.Fatalf("%s encoded (0x%08x, 0x%08x) decodes as %s",
			at.Format(time.RFC3339Nano), sec, extra, got.Format(time.RFC3339Nano))
	}
}

// The whole representable range round-trips at nanosecond resolution. A dense
// stride covers the interior; the edges get every nanosecond either side of
// each 2^31-second boundary, which is where the base wraps and the epoch moves.
func TestEncodeDecodeTimeRoundTrip(t *testing.T) {
	lo, hi := MinTime.Unix(), MaxTime.Unix()
	const samples = 1 << 20
	stride := (hi - lo) / samples
	rng := rand.New(rand.NewPCG(1, 2))
	for s := lo; s <= hi; s += stride {
		roundTrip(t, time.Unix(s, rng.Int64N(1e9)).UTC())
	}

	for k := int64(-1); k <= 7; k++ {
		edge := time.Unix(k<<31, 0).UTC()
		for _, d := range []time.Duration{-time.Second, -1, 0, 1, time.Second} {
			at := edge.Add(d)
			if at.Before(MinTime) || at.After(MaxTime) {
				continue
			}
			roundTrip(t, at)
		}
	}
	roundTrip(t, MinTime)
	roundTrip(t, MaxTime)
}

func TestEncodeTimeClampsOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		in, want time.Time
	}{
		{MinTime.Add(-1), MinTime},
		{time.Time{}, MinTime},
		{MaxTime.Add(1), MaxTime},
		{time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), MaxTime},
	} {
		if got := DecodeTime(EncodeTime(tc.in)); !got.Equal(tc.want) {
			t.Errorf("EncodeTime(%s) decodes as %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestDecodeTimeZeroIsEpochButInodeZeroIsUnset(t *testing.T) {
	if got := DecodeTime(0, 0); !got.Equal(time.Unix(0, 0)) {
		t.Errorf("DecodeTime(0, 0) = %s, want 1970-01-01T00:00:00Z", got)
	}
	if got := decodeTime(0, 0); !got.IsZero() {
		t.Errorf("inode decode of (0, 0) = %s, want the zero time", got)
	}
}

// ---------------------------------------------------------------------------
// geometry
// ---------------------------------------------------------------------------

func TestHasSuperBackup(t *testing.T) {
	sparse := map[uint32]bool{0: true, 1: true, 3: true, 5: true, 7: true, 9: true,
		25: true, 27: true, 49: true, 81: true, 125: true, 243: true, 343: true}
	for g := uint32(0); g < 400; g++ {
		if got := HasSuperBackup(g, true, false, [2]uint32{}); got != sparse[g] {
			t.Errorf("sparse_super group %d = %v, want %v", g, got, sparse[g])
		}
		if !HasSuperBackup(g, false, false, [2]uint32{}) {
			t.Errorf("without sparse_super, group %d has no backup", g)
		}
		want2 := g == 0 || g == 1 || g == 399
		if got := HasSuperBackup(g, true, true, [2]uint32{1, 399}); got != want2 {
			t.Errorf("sparse_super2 {1, 399} group %d = %v, want %v", g, got, want2)
		}
		if got := HasSuperBackup(g, true, true, [2]uint32{}); got != (g == 0) {
			t.Errorf("sparse_super2 with no backups, group %d = %v", g, got)
		}
	}
	// Powers above 2^31 must not overflow into a false match.
	if HasSuperBackup(0xFFFFFFFF, true, false, [2]uint32{}) {
		t.Error("group 0xFFFFFFFF reported as a power of 3, 5 or 7")
	}
	if !HasSuperBackup(3486784401, true, false, [2]uint32{}) { // 3^20
		t.Error("group 3^20 has no backup")
	}
}

func TestDirRecLen(t *testing.T) {
	for _, tc := range []struct{ name, want int }{
		{0, 8}, {1, 12}, {4, 12}, {5, 16}, {8, 16}, {9, 20}, {255, 264},
	} {
		if got := DirRecLen(tc.name); got != tc.want {
			t.Errorf("DirRecLen(%d) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// checksums
// ---------------------------------------------------------------------------

// The real image has generation 0 everywhere, so these vectors use a nonzero
// generation and inode number, and compare the chained codecs against the
// plainer statement of the same rule: hash a copy with the checksum zeroed.

func filled(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)*7 + seed
	}
	return b
}

func le32Bytes(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func TestInodeChecksumMatchesZeroedCopy(t *testing.T) {
	var uuid [16]byte
	copy(uuid[:], "0123456789abcdef")
	const ino, gen = 4242, 0xDEADBEEF

	for _, tc := range []struct {
		size, extra int
		hasHi       bool
	}{
		{128, 0, false},
		{256, 0, false},
		{256, 2, false}, // reaches i_checksum_hi's start but not its end
		{256, 4, true},
		{256, 32, true},
		{512, 32, true},
	} {
		raw := filled(tc.size, 3)
		binary.LittleEndian.PutUint32(raw[InodeOffGeneration:], gen)
		if tc.size > GoodOldInodeSize {
			binary.LittleEndian.PutUint16(raw[InodeOffExtraISize:], uint16(tc.extra))
		}
		want := inodeChecksumForTest(uuid, ino, raw, tc.hasHi)
		lo, hi, hasHi := InodeChecksum(MetadataSeed(uuid, 0, false), ino, gen, raw, tc.size)
		if hasHi != tc.hasHi {
			t.Errorf("size %d extra %d: hasHi = %v, want %v", tc.size, tc.extra, hasHi, tc.hasHi)
		}
		if lo != uint16(want) {
			t.Errorf("size %d extra %d: lo = 0x%04x, want 0x%04x", tc.size, tc.extra, lo, uint16(want))
		}
		if tc.hasHi && hi != uint16(want>>16) {
			t.Errorf("size %d extra %d: hi = 0x%04x, want 0x%04x", tc.size, tc.extra, hi, uint16(want>>16))
		}

		// Stamping and verifying through the parser agrees too.
		binary.LittleEndian.PutUint16(raw[InodeOffChecksumLo:], lo)
		if hasHi {
			binary.LittleEndian.PutUint16(raw[InodeOffChecksumHi:], hi)
		}
		fs := &FS{sb: Superblock{FeatureROCompat: ROCompatMetadataCsum, UUID: uuid}}
		if err := fs.verifyInodeChecksum(ino, raw); err != nil {
			t.Errorf("size %d extra %d: stamped inode does not verify: %v", tc.size, tc.extra, err)
		}
	}
}

func TestGroupDescChecksumMatchesZeroedCopy(t *testing.T) {
	const seed, group = 0x12345678, 77
	for _, size := range []int{GroupDescSizeMin, GroupDescSize64Bit} {
		raw := filled(size, 9)
		zeroed := bytes.Clone(raw)
		zeroed[GdOffChecksum], zeroed[GdOffChecksum+1] = 0, 0
		want := uint16(CRC32C(CRC32C(seed, le32Bytes(group)), zeroed))
		if got := GroupDescChecksum(seed, group, raw, size); got != want {
			t.Errorf("size %d: GroupDescChecksum = 0x%04x, want 0x%04x", size, got, want)
		}
	}
}

func TestMetadataSeed(t *testing.T) {
	var uuid [16]byte
	copy(uuid[:], "fedcba9876543210")
	if got, want := MetadataSeed(uuid, 0xAABBCCDD, false), CRC32C(^uint32(0), uuid[:]); got != want {
		t.Errorf("without CSUM_SEED = 0x%08x, want crc32c(~0, uuid) 0x%08x", got, want)
	}
	if got := MetadataSeed(uuid, 0xAABBCCDD, true); got != 0xAABBCCDD {
		t.Errorf("with CSUM_SEED = 0x%08x, want the stored seed", got)
	}
	// The kernel uses the stored seed whatever its value, zero included.
	if got := MetadataSeed(uuid, 0, true); got != 0 {
		t.Errorf("with CSUM_SEED and a zero seed = 0x%08x, want 0", got)
	}
}

func TestTailChecksumsUseInodeSeed(t *testing.T) {
	const seed, ino, gen = 0x0BADF00D, 31337, 0x01020304
	iseed := CRC32C(CRC32C(seed, le32Bytes(ino)), le32Bytes(gen))

	// An extent block whose header says 4 slots: the tail follows slot 4, at
	// byte 60, whatever eh_entries says.
	block := filled(1024, 1)
	copy(block, extentHeader(1, 4, 0))
	want := CRC32C(iseed, block[:ExtentHeaderSize+4*ExtentEntrySize])
	if got := ExtentTailChecksum(seed, ino, gen, block); got != want {
		t.Errorf("ExtentTailChecksum = 0x%08x, want 0x%08x", got, want)
	}
	// eh_max past the block leaves no room for a tail.
	binary.LittleEndian.PutUint16(block[4:], 1000)
	if got := ExtentTailChecksum(seed, ino, gen, block); got != 0 {
		t.Errorf("ExtentTailChecksum with no room for the tail = 0x%08x, want 0", got)
	}

	dir := filled(4096, 5)
	want = CRC32C(iseed, dir[:4096-DirTailSize])
	if got := DirTailChecksum(seed, ino, gen, dir); got != want {
		t.Errorf("DirTailChecksum = 0x%08x, want 0x%08x", got, want)
	}

	bitmap := filled(128, 2)
	if got, want := BitmapChecksum(seed, bitmap), CRC32C(seed, bitmap); got != want {
		t.Errorf("BitmapChecksum = 0x%08x, want 0x%08x", got, want)
	}
}
