package libext

import "time"

// Inode timestamps.
//
// An ext4 timestamp is two words. The base is the classic 32-bit i_*time field,
// read as a *signed* count of seconds from 1970. On an inode large enough to
// carry it, a companion "extra" word widens it: bits 0-1 are an epoch count,
// added to the base in units of 2^32 seconds, and bits 2-31 are nanoseconds.
// That gives a range of 2^34 seconds starting at 1901-12-13T20:45:52Z.

// MinTime and MaxTime bound what an ext4 inode timestamp can represent: the
// signed 32-bit base at its minimum with epoch 0, and at its maximum with
// epoch 3 and the largest nanosecond count.
var (
	MinTime = time.Unix(-1<<31, 0).UTC()
	MaxTime = time.Unix(1<<31-1+3<<32, 999999999).UTC()
)

// EncodeTime is the kernel's ext4_encode_extra_time: it splits t into the base
// seconds word and the extra word as an inode stores them.
//
// A time outside [MinTime, MaxTime] is clamped to the nearer bound first, as
// the kernel's timestamp_truncate does before encoding. That includes the zero
// time.Time, which is year 1 and so encodes as MinTime; a caller using the zero
// value to mean "not set" must test for it before calling.
//
// Without the extra word - on a 128-byte inode, or for i_dtime - only sec is
// stored, which represents 1901 to 2038 at whole-second resolution.
func EncodeTime(t time.Time) (sec uint32, extra uint32) {
	if t.Before(MinTime) {
		t = MinTime
	} else if t.After(MaxTime) {
		t = MaxTime
	}
	s := t.Unix()
	epoch := uint32((s-int64(int32(s)))>>32) & 0x3
	return uint32(s), epoch | uint32(t.Nanosecond())<<2
}

// DecodeTime is the kernel's ext4_decode_extra_time: the base read as signed
// seconds, plus the epoch from extra in units of 2^32 seconds, plus its
// nanoseconds. The result is in UTC.
//
// So 0x80000000 with epoch 0 is 1901-12-13T20:45:52Z, and with epoch 1 is
// 2038-01-19T03:14:08Z. A nanosecond count of 10^9 or more cannot come from
// the kernel; it is dropped rather than allowed to carry into the seconds.
//
// DecodeTime(0, 0) is 1970-01-01T00:00:00Z, as the kernel reads it. The Inode
// time fields differ on exactly that one input; see Inode.
func DecodeTime(sec uint32, extra uint32) time.Time {
	seconds := int64(int32(sec)) + int64(extra&0x3)<<32
	nsec := int64(extra >> 2)
	if nsec >= 1e9 {
		// Corrupt or non-conforming; keep the seconds rather than skewing them.
		nsec = 0
	}
	return time.Unix(seconds, nsec).UTC()
}

// decodeTime is DecodeTime for the parsed Inode, which reports a timestamp
// whose words are both zero as the zero time.Time.
//
// Both words zero is what an unset field looks like - a creation time on an
// inode written by a tool that never set one - and reporting it as 1970 puts a
// false date in front of a reader. The price is that a file genuinely stamped
// 1970-01-01T00:00:00Z exactly is reported as unset too.
//
// Treating the base as unsigned once an epoch is present, as this package once
// did, puts every date whose base has its high bit set (2038 to 2106, 2174 to
// 2242, 2310 to 2378) a further 2^32 seconds (136 years) into the future - a
// plausible-looking wrong answer rather than an obvious one. DecodeTime adds
// the epoch to the signed base instead.
func decodeTime(sec uint32, extra uint32) time.Time {
	if sec == 0 && extra == 0 {
		return time.Time{}
	}
	return DecodeTime(sec, extra)
}
