package libext

import (
	"fmt"
	"io"
	iofs "io/fs"
)

// Options configures how an image is opened and parsed.
//
// The zero value is valid and is what Open uses. It differs from the historical
// behaviour of this package in two ways: the image size is probed from the
// reader when ImageSize is 0, and features that would make libext report
// incorrect offsets are refused rather than parsed optimistically. Set
// Permissive to restore best-effort parsing of those images.
type Options struct {
	// ImageSize bounds every read. When 0, OpenWithOptions probes the reader for
	// a Size() int64 or Stat() method; if neither is available the image is
	// treated as unbounded and no bounds checks are performed.
	ImageSize uint64

	// BaseOffset is added to every absolute disk offset the library reports. Set
	// it to the partition start when the reader is volume-relative so reported
	// offsets are image-absolute.
	BaseOffset int64

	// Permissive downgrades unsupported- and unknown-feature errors to warnings,
	// and makes directory parsing resynchronise past damaged records instead of
	// stopping at the first one. Use it for damaged or exotic images where a
	// partial answer beats no answer. Offsets reported for an image opened
	// permissively may be wrong; check Warnings.
	Permissive bool

	// VerifyChecksums promotes a metadata checksum mismatch from a diagnostic
	// warning to a hard error.
	VerifyChecksums bool

	// MaxExtents caps the number of extent records parsed for a single inode.
	// 0 selects the library default.
	MaxExtents int
}

// WarningCode classifies a non-fatal condition observed while parsing.
type WarningCode uint8

const (
	// WarnUnknownFeature reports a feature bit libext does not describe.
	WarnUnknownFeature WarningCode = iota + 1
	// WarnUnsupportedFeature reports a known feature libext cannot honour.
	WarnUnsupportedFeature
	// WarnChecksumMismatch reports metadata whose stored checksum did not verify.
	WarnChecksumMismatch
	// WarnTruncatedImage reports that the reader is smaller than the filesystem
	// the superblock describes.
	WarnTruncatedImage
	// WarnDegradedRead reports a structure parsed only partially.
	WarnDegradedRead
)

func (c WarningCode) String() string {
	switch c {
	case WarnUnknownFeature:
		return "unknown_feature"
	case WarnUnsupportedFeature:
		return "unsupported_feature"
	case WarnChecksumMismatch:
		return "checksum_mismatch"
	case WarnTruncatedImage:
		return "truncated_image"
	case WarnDegradedRead:
		return "degraded_read"
	default:
		return "unknown"
	}
}

// Warning is a non-fatal condition recorded during parsing. Warnings never stop
// a read; they record where the answer may be incomplete or wrong.
type Warning struct {
	Code    WarningCode
	Feature string // feature name when the warning concerns one, else empty
	Detail  string
}

func (w Warning) String() string {
	if w.Feature == "" {
		return fmt.Sprintf("%s: %s", w.Code, w.Detail)
	}
	return fmt.Sprintf("%s: %s: %s", w.Code, w.Feature, w.Detail)
}

// probeReaderSize determines the readable extent of r.
//
// Size is checked before Stat deliberately: io.SectionReader is the shape used
// when a volume is presented as a slice of a larger image, and it exposes Size
// but not Stat. Probing Stat alone left every SectionReader-backed volume with
// an image size of 0, which disabled bounds checking entirely.
func probeReaderSize(r io.ReaderAt) uint64 {
	if sr, ok := r.(interface{ Size() int64 }); ok {
		if n := sr.Size(); n > 0 {
			return uint64(n)
		}
	}
	if st, ok := r.(interface {
		Stat() (iofs.FileInfo, error)
	}); ok {
		info, err := st.Stat()
		if err == nil && !info.IsDir() && info.Size() > 0 {
			return uint64(info.Size())
		}
	}
	return 0
}
