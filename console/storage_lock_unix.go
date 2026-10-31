//go:build unix

package console

import (
	"fmt"
	"os"
	"syscall"
)

func lockStorage(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("storage: directory already owned or locking unavailable: %w", err)
	}
	return func() error { return f.Close() }, nil
}
