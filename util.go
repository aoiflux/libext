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
