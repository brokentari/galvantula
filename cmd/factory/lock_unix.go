//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"syscall"
)

// lockDB holds an exclusive lock on db until the returned file is closed or the process exits.
func lockDB(db string) (*os.File, error) {
	f, err := os.OpenFile(db+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another factory server uses %s: %w", db, err)
	}
	return f, nil
}
