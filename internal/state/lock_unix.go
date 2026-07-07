//go:build unix

package state

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory flock on path (created if
// missing) and returns the unlock func. Blocks until the lock is
// available — state mutations are milliseconds, so waiting beats
// failing.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
