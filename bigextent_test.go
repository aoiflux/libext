package libext

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// sparseImage is an io.ReaderAt over a decompressed image that keeps only its
// nonzero chunks, so a 128 MiB image of mostly zeros costs a few KiB to hold.
type sparseImage struct {
	size   int64
	chunks map[int64][]byte
}

const sparseChunk = 4096

func loadSparseImage(t testing.TB, path string) *sparseImage {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open reference image: %v", err)
	}
	defer f.Close()
	r := bzip2.NewReader(f)
	img := &sparseImage{chunks: map[int64][]byte{}}
	zero := make([]byte, sparseChunk)
	for {
		buf := make([]byte, sparseChunk)
		n, err := io.ReadFull(r, buf)
		if n > 0 && !bytes.Equal(buf[:n], zero[:n]) {
			img.chunks[img.size/sparseChunk] = buf[:n]
		}
		img.size += int64(n)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return img
		}
		if err != nil {
			t.Fatalf("bunzip2 reference image: %v", err)
		}
	}
}

func (s *sparseImage) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < s.size {
		i, at := off/sparseChunk, off%sparseChunk
		span := min(int64(len(p)-n), sparseChunk-at, s.size-off)
		if c, ok := s.chunks[i]; ok && at < int64(len(c)) {
			copy(p[n:n+int(span)], c[at:])
		} else {
			clear(p[n : n+int(span)])
		}
		n += int(span)
		off += span
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *sparseImage) Size() int64 { return s.size }

// A 128 MiB file written as one extent of 32768 blocks, the kernel's longest,
// with ee_len 0x8000 on disk. Before EXT-3 was fixed this extent was read as
// "unwritten, 0 blocks" and the whole file came back as zeros.
func TestRealImageMaxLengthWrittenExtent(t *testing.T) {
	fs, err := OpenWithOptions(loadSparseImage(t, "testdata/e2fs_bigextent.img.bz2"), Options{Strict: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	inode := lookupIno(t, fs, "/big.bin")
	const blocks = ExtentInitMaxLen
	bs := int64(fs.Superblock().BlockSize)

	if got := binary.LittleEndian.Uint16(inode.BlockRaw[ExtentHeaderSize+4:]); got != 0x8000 {
		t.Fatalf("on-disk ee_len = 0x%04x, want 0x8000", got)
	}
	exts, err := fs.InodeExtents(inode, ExtentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 || exts[0].LogicalBlock != 0 || exts[0].Blocks != blocks || exts[0].Unwritten() || exts[0].Sparse() {
		t.Fatalf("extents = %+v, want one written extent of %d blocks at logical 0", exts, blocks)
	}

	f, err := fs.Open(inode.Number)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		block int64
		fill  byte
	}{
		{"first block", 0, 0xA5},
		{"middle block", blocks / 2, 0},
		{"last block", blocks - 1, 0x5A},
	} {
		got := make([]byte, bs)
		if _, err := f.ReadAt(got, tc.block*bs); err != nil && err != io.EOF {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !bytes.Equal(got, bytes.Repeat([]byte{tc.fill}, int(bs))) {
			t.Errorf("%s: does not hold 0x%02X throughout", tc.name, tc.fill)
		}
	}
}
