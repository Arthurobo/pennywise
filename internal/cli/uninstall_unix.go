//go:build !windows

package cli

import "os"

// removeRunningBinary deletes the running binary on disk. On Unix this
// is straightforward — the kernel keeps the in-memory image alive until
// the process exits, so deleting the file mid-execution is safe.
func removeRunningBinary(path string) error {
	return os.Remove(path)
}
