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

func Unlock(fd uintptr) error {
	handle := windows.Handle(fd)
	var overlapped windows.Overlapped
	if err := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("failed to release managed identities pool state file lock: %w", err)
	}
	return nil
}
