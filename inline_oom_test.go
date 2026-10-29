package libext

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// PAR-35: an inline inode's claimed size must be checked before it is used as
// an allocation size.
//
// Inline data lives inside the inode, so the inode's own capacity bounds it.
// Reserving i_size bytes instead means one flipped bit in i_size_high asks for
// 192 GiB, and the process dies with "fatal error: runtime: out of memory" -
// which no recover can catch. That is why the cases run in a child process:
// the unfixed library cannot be exercised in the test's own process without
// taking the test binary down with it.

// par35ChildEnv marks the re-executed child, so the child runs the cases
// rather than spawning a child of its own.
const par35ChildEnv = "LIBEXT_PAR35_CHILD"

// TestForgedInlineSizeDoesNotExhaustMemory re-executes this test in a child
// process and requires the child to exit cleanly.
//
// Three outcomes are possible on an unfixed library and all three fail here:
// the allocation is refused by the OS and the child dies of out of memory; it
// exceeds the runtime's maximum and the child panics; or an overcommitting
// kernel grants it, and the child then fails the assertions below because no
// error was returned and the allocation budget was blown.
func TestForgedInlineSizeDoesNotExhaustMemory(t *testing.T) {
	if os.Getenv(par35ChildEnv) == "1" {
		forgedInlineCases(t)
		return
	}

	// The child's output goes to a file rather than to a pipe the parent
	// buffers. An unfixed child exhausts the system commit limit, and a parent
	// that needs to allocate while that is true dies with it - reporting
	// nothing. Nothing is allocated here until the child has exited and
	// released its charge.
	logPath := filepath.Join(t.TempDir(), "child.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create child log: %v", err)
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestForgedInlineSizeDoesNotExhaustMemory$", "-test.v")
	cmd.Env = append(os.Environ(), par35ChildEnv+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	runErr := cmd.Run()
	_ = logFile.Close()

	out, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read child log: %v", err)
	}
	if runErr != nil {
		t.Fatalf("child process failed (%v); a forged inline size must be refused "+
			"before it is allocated:\n%s", runErr, firstLines(out, 12))
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("child process did not report PASS:\n%s", firstLines(out, 12))
	}
}

// firstLines trims a child's output to its decisive head: a fatal out-of-memory
// dump is thousands of lines of runtime stack, and the first few say everything.
func firstLines(b []byte, n int) string {
	lines := strings.SplitN(string(b), "\n", n+1)
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}

// forgedInlineCases is the body that runs in the child.
func forgedInlineCases(t *testing.T) {
	// Both option sets are exercised, because the refusal must not depend on
	// either: Permissive is what a recovery tool opens with - DUES does - and
	// it downgrades unsupported-feature refusals to warnings. A size no inode
	// can hold is corruption rather than an exotic layout, so it is refused
	// whatever the setting.
	optsets := []struct {
		name string
		opts Options
	}{
		{"default", Options{}},
		{"permissive", Options{Permissive: true}},
	}

	// The fixtures are built before the measurement starts: a 1 MiB fixture
	// image would swamp the budget the calls under test are held to.
	unitRaw := forgedInlineInode(t)
	type fixture struct {
		name   string
		fileFS *FS
		fileIn uint32
		dirFS  *FS
		dirIn  uint32
	}
	var fixtures []fixture
	for _, o := range optsets {
		fileFS, fileIn := forgedInlineFileFixture(t, o.opts)
		dirFS, dirIn := forgedInlineDirFixture(t, o.opts)
		fixtures = append(fixtures, fixture{o.name, fileFS, fileIn, dirFS, dirIn})
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// 1. The unit repro: a 256-byte inode claiming 192 GiB.
	unit := parseInode(unitRaw, 14)
	if want := uint64(0x30)<<32 | 19; unit.Size != want {
		t.Fatalf("fixture Size = %d, want %d; i_size_high is not being read", unit.Size, want)
	}
	if _, err := (&FS{}).inlineDataFromRaw(unit, unitRaw); err == nil {
		t.Errorf("inlineDataFromRaw accepted an inode claiming %d bytes of inline data", unit.Size)
	} else if !errors.Is(err, ErrUnsupportedLayout) {
		t.Errorf("inlineDataFromRaw error = %v, want one wrapping ErrUnsupportedLayout", err)
	}

	for _, fx := range fixtures {
		// 2. The same forgery through the whole-file read path. The inline
		// branch in readInodeDataMapped runs before the readAllLimit check, so
		// nothing else on this path bounds the size.
		if _, err := fx.fileFS.ReadFile(fx.fileIn); err == nil {
			t.Errorf("%s: ReadFile accepted an inline file claiming 192 GiB", fx.name)
		} else if !errors.Is(err, ErrUnsupportedLayout) {
			t.Errorf("%s: ReadFile error = %v, want one wrapping ErrUnsupportedLayout", fx.name, err)
		}

		// 3. An inline directory. Its size is the low word alone - i_size_high
		// is i_dir_acl there - so 4 GiB is the worst it can claim.
		if _, err := fx.dirFS.ListDir(fx.dirIn); err == nil {
			t.Errorf("%s: ListDir accepted an inline directory claiming 4 GiB", fx.name)
		} else if !errors.Is(err, ErrUnsupportedLayout) {
			t.Errorf("%s: ListDir error = %v, want one wrapping ErrUnsupportedLayout", fx.name, err)
		}

		// 4. And through InlineData, the accessor DUES calls directly.
		if _, _, err := fx.fileFS.InlineData(fx.fileIn); err == nil {
			t.Errorf("%s: InlineData accepted an inline file claiming 192 GiB", fx.name)
		} else if !errors.Is(err, ErrUnsupportedLayout) {
			t.Errorf("%s: InlineData error = %v, want one wrapping ErrUnsupportedLayout", fx.name, err)
		}
	}

	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Errorf("the refusals allocated %d bytes, over the 1 MiB budget: "+
			"the claimed size is still reaching an allocation", grew)
	}
}

// forgedInlineInode is the DUES fuzz finding at unit scale: a valid 19-byte
// inline file whose i_size_high byte has been set to 0x30, so i_size reads as
// 192 GiB.
func forgedInlineInode(t testing.TB) []byte {
	t.Helper()
	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, 19, InodeFlagInlineData,
		[]byte("tiny inline content"), nil)
	raw[InodeOffSizeHi] = 0x30
	return raw
}

// forgedInlineFileFixture is the same forgery in an image, reachable through
// ReadFile.
func forgedInlineFileFixture(t testing.TB, opts Options) (*FS, uint32) {
	t.Helper()
	const inodeNum = 40

	img := buildTestImage(t, defaultSBConfig())
	content := "inline file body"
	writeTestInode(img, inodeNum, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, []byte(content))

	off := testInodeTableBlock*testBlockSize + (inodeNum-1)*testInodeSize
	img[off+InodeOffSizeHi] = 0x30

	return openFixture(t, img, opts), inodeNum
}

// forgedInlineDirFixture is an inline directory whose size claims 4 GiB.
func forgedInlineDirFixture(t testing.TB, opts Options) (*FS, uint32) {
	t.Helper()
	const (
		dirInode  = 40
		fileInode = 41
	)

	img := buildTestImage(t, defaultSBConfig())
	var inline []byte
	inline = binary.LittleEndian.AppendUint32(inline, RootInode)
	inline = append(inline, dirent(fileInode, 20, 1, "nested.txt")...)

	writeTestInode(img, dirInode, ModeDir|0o755, uint32(len(inline)),
		InodeFlagInlineData, inline)
	writeTestInode(img, fileInode, ModeRegular|0o644, 0, 0, nil)

	off := testInodeTableBlock*testBlockSize + (dirInode-1)*testInodeSize
	binary.LittleEndian.PutUint32(img[off+InodeOffSizeLo:], 0xFFFFFFFF)

	return openFixture(t, img, opts), dirInode
}
