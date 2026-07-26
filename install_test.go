package goselfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReplaceBinaryWritesNewContent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(target, []byte("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Errorf("got %q, want new", content)
	}
}

// An install into a directory the user made executable-only for their group,
// or any other non-default mode, must keep that mode rather than resetting it.
func TestReplaceBinaryPreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}

	for _, mode := range []os.FileMode{0o700, 0o755, 0o775} {
		target := filepath.Join(t.TempDir(), "tool")
		if err := os.WriteFile(target, []byte("old"), mode); err != nil {
			t.Fatal(err)
		}
		// os.WriteFile applies the process umask, which would silently reduce
		// 0775 to 0755 and make this assert the wrong starting mode.
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}

		if err := replaceBinary(target, []byte("new")); err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}

		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("got mode %v, want %v", info.Mode().Perm(), mode)
		}
	}
}

// A new install with no existing file still has to end up executable.
func TestReplaceBinaryDefaultsToExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}

	target := filepath.Join(t.TempDir(), "tool")

	if err := replaceBinary(target, []byte("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("got mode %v, want 0755", info.Mode().Perm())
	}
}

// A failure must not leave a partial temporary file next to the binary.
func TestReplaceBinaryCleansUpOnFailure(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "missing-subdirectory", "tool")

	if err := replaceBinary(target, []byte("new")); err == nil {
		t.Fatal("expected an error")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d entries behind", len(entries))
	}
}

func TestCleanupOldBinaryIsSafeWhenAbsent(t *testing.T) {
	if err := CleanupOldBinary(); err != nil {
		t.Errorf("CleanupOldBinary: %v", err)
	}
}

func TestSwapFileReplacesTarget(t *testing.T) {
	directory := t.TempDir()
	staged := filepath.Join(directory, "staged")
	target := filepath.Join(directory, "tool")

	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := swapFile(staged, target); err != nil {
		t.Fatalf("swapFile: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Errorf("got %q, want new", content)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged file was not consumed")
	}
}
