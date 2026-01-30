//go:build !windows

package framework

import (
	"fmt"
	"syscall"
)

func (state *leasedIdentityPoolState) lock() error {
	if err := syscall.Flock(int(state.lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

func (state *leasedIdentityPoolState) unlock() error {
	if err := syscall.Flock(int(state.lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("failed to release managed identities pool state file lock: %w", err)
	}
	return nil
}
