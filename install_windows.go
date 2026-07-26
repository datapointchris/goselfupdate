//go:build windows

package goselfupdate

import "os"

// swapFile moves the staged binary over the target.
//
// Windows holds an open handle on a running executable, so renaming over it
// fails with a sharing violation. Renaming the running binary away is however
// permitted, so the old file is displaced first and the new one takes its
// name. The displaced copy cannot be deleted until the process exits; it is
// removed by [CleanupOldBinary] on the next start.
func swapFile(staged, target string) error {
	previous := target + rollbackSuffix

	// A leftover from an earlier update would block the rename below.
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(target, previous); err != nil {
		return err
	}

	if err := os.Rename(staged, target); err != nil {
		// Put the working binary back rather than leaving nothing installed.
		if rollback := os.Rename(previous, target); rollback != nil {
			return rollback
		}
		return err
	}

	return nil
}
