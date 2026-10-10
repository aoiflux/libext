package libext

import (
	"encoding/binary"
	"strings"
	"testing"
)

// setTestInodeBlocks writes i_blocks (in 512-byte sectors) and i_file_acl for a
// fixture inode, the two fields the fast-symlink test is decided on.
func setTestInodeBlocks(img []byte, num uint32, sectors uint32, fileACL uint32) {
	off := testInodeTableBlock*testBlockSize + int(num-1)*testInodeSize
	binary.LittleEndian.PutUint32(img[off+InodeOffBlocksLo:], sectors)
	binary.LittleEndian.PutUint32(img[off+InodeOffFileACLLo:], fileACL)
}

// readLink opens an inode and reads its target.
func readLink(t *testing.T, fs *FS, ino uint32) string {
	t.Helper()
	f, err := fs.Open(ino)
	if err != nil {
		t.Fatalf("Open(%d): %v", ino, err)
	}
	got, err := f.ReadLink()
	if err != nil {
		t.Fatalf("ReadLink(%d): %v", ino, err)
	}
	return got
}

// The kernel keeps a symlink target in i_block only when strlen+1 fits in the
// 60 bytes there (fs/ext4/namei.c ext4_symlink), so the longest inline target
// is 59 bytes. A 60-byte target lives in a data block, and its i_block holds
// that block's map.
func TestReadLinkInlineAt59Bytes(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	target := strings.Repeat("a", 59)
	root := make([]byte, 60)
	copy(root, target)
	writeTestInode(img, 20, 0xA1FF, uint32(len(target)), 0, root)

	fs := openFixture(t, img, Options{})
	if got := readLink(t, fs, 20); got != target {
		t.Errorf("ReadLink = %q, want %q", got, target)
	}
}

func TestReadLinkExtentMappedAt60Bytes(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	target := strings.Repeat("b", 60)
	writeTestBlock(img, 100, []byte(target))
	root := inodeRoot(extentHeader(1, 4, 0), extentLeafEntry(0, 1, 100))
	writeTestInode(img, 20, 0xA1FF, uint32(len(target)), InodeFlagExtents, root)
	setTestInodeBlocks(img, 20, testBlockSize/512, 0)

	fs := openFixture(t, img, Options{})
	got := readLink(t, fs, 20)
	if strings.HasPrefix(got, "\x0a\xf3") {
		t.Fatalf("ReadLink returned the extent header as the target: %q", got)
	}
	if got != target {
		t.Errorf("ReadLink = %q, want %q", got, target)
	}
}

// An ext2/ext3 slow symlink: no extents flag, a classic block pointer in
// i_block, and a 60-byte target. Reading i_block as text returns the pointer.
func TestReadLinkBlockMappedAt60Bytes(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	target := strings.Repeat("c", 60)
	writeTestBlock(img, 101, []byte(target))
	writeTestInode(img, 21, 0xA1FF, uint32(len(target)), 0, classicRoot([]uint32{101}, 0, 0, 0))
	setTestInodeBlocks(img, 21, testBlockSize/512, 0)

	fs := openFixture(t, img, Options{})
	if got := readLink(t, fs, 21); got != target {
		t.Errorf("ReadLink = %q, want %q", got, target)
	}
}

// A tool that leaves i_blocks at zero still cannot have stored a 60-byte target
// inline, so the length decides.
func TestReadLinkSixtyBytesWithZeroBlockCountIsNotInline(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	target := strings.Repeat("d", 60)
	writeTestBlock(img, 102, []byte(target))
	root := inodeRoot(extentHeader(1, 4, 0), extentLeafEntry(0, 1, 102))
	writeTestInode(img, 22, 0xA1FF, uint32(len(target)), InodeFlagExtents, root)

	fs := openFixture(t, img, Options{})
	if got := readLink(t, fs, 22); got != target {
		t.Errorf("ReadLink = %q, want %q", got, target)
	}
}

// An external xattr block is counted in i_blocks but holds no part of the
// target; the kernel subtracts it before deciding (ext4_inode_is_fast_symlink).
func TestReadLinkInlineWithXattrBlock(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	target := "/short/target"
	root := make([]byte, 60)
	copy(root, target)
	writeTestInode(img, 23, 0xA1FF, uint32(len(target)), 0, root)
	setTestInodeBlocks(img, 23, testBlockSize/512, 200)

	fs := openFixture(t, img, Options{})
	if got := readLink(t, fs, 23); got != target {
		t.Errorf("ReadLink = %q, want %q", got, target)
	}
	exts, err := fs.Extents(23)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	if len(exts) != 0 {
		t.Errorf("fast symlink with an xattr block reported extents: %+v", exts)
	}
}
