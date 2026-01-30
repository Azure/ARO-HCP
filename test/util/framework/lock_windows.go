//go:build windows

package framework

import (
	"fmt"
	"syscall"
)

func (state *leasedIdentityPoolState) lock() error {
	handle := syscall.Handle(state.lockFile.Fd())
	var overlapped syscall.Overlapped
	if err := syscall.LockFileEx(handle, syscall.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

func (state *leasedIdentityPoolState) unlock() error {
	handle := syscall.Handle(state.lockFile.Fd())
	var overlapped syscall.Overlapped
	if err := syscall.UnlockFileEx(handle, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("failed to release managed identities pool state file lock: %w", err)
	}
	return nil
}
