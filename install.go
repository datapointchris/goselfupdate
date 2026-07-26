package goselfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceBinary installs new bytes over an existing executable.
//
// The temporary file is created in the target's own directory because a rename
// cannot cross filesystems, and rename is what makes the installation atomic:
// a reader either sees the whole old file or the whole new one, never a
// half-written binary.
func replaceBinary(target string, binary []byte) error {
	directory := filepath.Dir(target)

	mode := os.FileMode(0o755)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}

	temporary, err := os.CreateTemp(directory, ".goselfupdate-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", directory, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if _, err := temporary.Write(binary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", temporaryName, err)
	}
	// Flush to disk before the rename, so a crash cannot leave the target
	// pointing at an inode whose contents were never persisted.
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync %s: %w", temporaryName, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", temporaryName, err)
	}
	if err := os.Chmod(temporaryName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", temporaryName, err)
	}

	if err := swapFile(temporaryName, target); err != nil {
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
	return nil
}

// rollbackSuffix names the displaced binary on platforms that cannot replace a
// running executable in place.
const rollbackSuffix = ".old"

// CleanupOldBinary removes the previous executable left behind by an update on
// platforms that cannot overwrite a running binary, currently only Windows.
//
// It is safe to call on every platform and on every start, and reports no
// error when there is nothing to remove. Calling it early in main keeps a
// stale copy from accumulating next to the installed binary.
func CleanupOldBinary() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	err = os.Remove(executable + rollbackSuffix)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
