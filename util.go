package libext

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

func trimCString(v []byte) string {
	if i := strings.IndexByte(string(v), 0); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(string(v))
}

func unixTime(sec uint32) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0).UTC()
}

// decodeTime combines a 32-bit ext timestamp with its ext4 "extra" companion.
//
// The extra word packs two things: bits 0-1 extend the seconds field beyond
// 2038, and bits 2-31 hold nanoseconds. The seconds field is signed, so a value
// with the high bit set is a pre-1970 date when no epoch bits are present, but
// a post-2038 date when they are — which is exactly what makes dropping the
// extra word produce plausible-looking wrong answers rather than obvious ones.
func decodeTime(sec uint32, extra uint32) time.Time {
	if sec == 0 && extra == 0 {
		return time.Time{}
	}

	seconds := int64(int32(sec))
	if epoch := int64(extra & 0x3); epoch != 0 {
		seconds = int64(sec) | (epoch << 32)
	}

	nsec := int64(extra >> 2)
	if nsec >= 1e9 {
		// Corrupt or non-conforming; keep the seconds rather than skewing them.
		nsec = 0
	}
	return time.Unix(seconds, nsec).UTC()
}

func checkBounds(off uint64, length uint64, max uint64) error {
	if max == 0 {
		return nil
	}
	if off > max || length > max-off {
		return fmt.Errorf("offset %d length %d out of bounds %d", off, length, max)
	}
	return nil
}

func le16(b []byte, off int) uint16 {
	return binary.LittleEndian.Uint16(b[off : off+2])
}

func le32(b []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(b[off : off+4])
}
