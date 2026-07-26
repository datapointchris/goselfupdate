//go:build !windows

package goselfupdate

import "os"

// swapFile moves the staged binary over the target.
//
// A running process on Unix holds its executable by inode, not by path, so
// renaming over it neither disturbs the running program nor fails. The old
// inode is released when the last process using it exits.
func swapFile(staged, target string) error {
	return os.Rename(staged, target)
}
