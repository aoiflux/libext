package libext

import (
	"encoding/binary"
	"testing"
	"time"
)

// kernelEncodeTime is ext4_encode_extra_time from fs/ext4/ext4.h, written out
// independently of the package so the decoder is checked against the kernel's
// rule rather than against its own inverse:
//
//	extra = ((tv_sec - (s32)tv_sec) >> 32) & EXT4_EPOCH_MASK | tv_nsec << 2
//	base  = (u32)tv_sec
func kernelEncodeTime(sec int64, nsec int64) (uint32, uint32) {
	epoch := uint32((sec-int64(int32(sec)))>>32) & 0x3
	return uint32(sec), epoch | uint32(nsec)<<2
}

// kernelTimeCases are the instants at every edge of ext4's 34-bit range: both
// ends, the 1970 origin, and each side of the 2038 and 2106 wraps of the base.
var kernelTimeCases = []struct {
	name string
	at   string
}{
	{"min", "1901-12-13T20:45:52Z"},
	{"pre-epoch", "1969-12-31T23:59:59Z"},
	{"epoch", "1970-01-01T00:00:00Z"},
	{"last-int32", "2038-01-19T03:14:07Z"},
	{"first-post-2038", "2038-01-19T03:14:08Z"},
	{"uint32-wrap", "2106-02-07T06:28:16Z"},
	{"epoch-2-high-base", "2200-01-01T00:00:00Z"},
	{"epoch-3-high-base", "2350-01-01T00:00:00Z"},
	{"max", "2446-05-10T22:38:55Z"},
}

func TestDecodeTimeMatchesKernelEncoding(t *testing.T) {
	for _, tc := range kernelTimeCases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			const nsec = 123456789
			want = want.Add(nsec)
			base, extra := kernelEncodeTime(want.Unix(), nsec)

			if got := decodeTime(base, extra); !got.Equal(want) {
				t.Errorf("decodeTime(0x%08x, 0x%08x) = %s, want %s",
					base, extra, got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
			}

			raw := rawInode(256, func(raw []byte) {
				binary.LittleEndian.PutUint32(raw[InodeOffMtime:], base)
				binary.LittleEndian.PutUint32(raw[InodeOffMtimeExtra:], extra)
				binary.LittleEndian.PutUint16(raw[InodeOffExtraISize:], 32)
			})
			if got := parseInode(raw, 12).Mtime; !got.Equal(want) {
				t.Errorf("Inode.Mtime = %s, want %s",
					got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
			}
		})
	}
}
