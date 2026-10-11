package libext

import (
	"encoding/binary"
	"testing"
	"time"
)

// A superblock time is ext4_get_tstamp's reading: the low word unsigned, plus
// the *_hi byte shifted above it. Unlike an inode time, the low word is never
// signed, so 0xFFFFFFFF with no high byte is 2106, not 1969.
func TestSuperblockTimesUseHighByte(t *testing.T) {
	fields := []struct {
		name   string
		lo, hi int
		get    func(Superblock) time.Time
	}{
		{"s_wtime", SbOffWriteTime, SbOffWriteTimeHi, func(sb Superblock) time.Time { return sb.WriteTime }},
		{"s_mtime", SbOffMountTime, SbOffMountTimeHi, func(sb Superblock) time.Time { return sb.MountTime }},
		{"s_mkfs_time", SbOffMkfsTime, SbOffMkfsTimeHi, func(sb Superblock) time.Time { return sb.MkfsTime }},
		{"s_lastcheck", SbOffLastCheck, SbOffLastCheckHi, func(sb Superblock) time.Time { return sb.LastCheck }},
	}
	cases := []struct {
		lo   uint32
		hi   uint8
		want time.Time
	}{
		{0x00000010, 1, time.Date(2106, 2, 7, 6, 28, 32, 0, time.UTC)},
		{0xFFFFFFFF, 0, time.Date(2106, 2, 7, 6, 28, 15, 0, time.UTC)},
		{1700000000, 0, time.Unix(1700000000, 0).UTC()},
		{0, 2, time.Unix(2<<32, 0).UTC()},
		{0, 0, time.Time{}},
	}
	for _, f := range fields {
		for _, tc := range cases {
			img := buildTestImage(t, defaultSBConfig())
			sb := img[SuperblockOffset:]
			binary.LittleEndian.PutUint32(sb[f.lo:], tc.lo)
			sb[f.hi] = tc.hi
			got := f.get(openFixture(t, img, Options{}).Superblock())
			if !got.Equal(tc.want) || got.IsZero() != tc.want.IsZero() {
				t.Errorf("%s lo=0x%08x hi=%d: got %s, want %s", f.name, tc.lo, tc.hi, got, tc.want)
			}
		}
	}
}

func TestRealImageMkfsTime(t *testing.T) {
	sb := openCsumImage(t, loadCsumImage(t), Options{}).Superblock()
	if want := time.Unix(1700000000, 0).UTC(); !sb.MkfsTime.Equal(want) {
		t.Errorf("MkfsTime = %s, want %s (E2FSPROGS_FAKE_TIME)", sb.MkfsTime, want)
	}
}
