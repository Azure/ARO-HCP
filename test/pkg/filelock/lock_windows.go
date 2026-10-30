//go:build windows

// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package filelock

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func Lock(fd uintptr) error {
	handle := windows.Handle(fd)
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

// TryLock acquires the exclusive lock without blocking. It returns ErrLockBusy
// when another holder currently owns the lock, so callers can poll instead of
// waiting indefinitely on a possibly stalled holder.
func TryLock(fd uintptr) error {
	handle := windows.Handle(fd)
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		if errors.Is(err, windows.ERROR_IO_PENDING) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return ErrLockBusy
		}
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

func Unlock(fd uintptr) error {
	handle := windows.Handle(fd)
	var overlapped windows.Overlapped
	if err := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("failed to release state file lock: %w", err)
	}
	return nil
}
