//go:build !windows

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
	"os"
	"path/filepath"
	"testing"
)

// The Windows variant (lock_windows.go) is compile-checked only
// (GOOS=windows go vet ./pkg/filelock/): this repo has no Windows CI, so its
// busy-lock mapping is verified by review. These tests pin the Unix mapping
// the identity pool and the cluster-creation pacer rely on: TryLock must
// report ErrLockBusy — not block, not a raw syscall error — while another open
// file description holds the lock, and succeed again once it is released.

// TestTryLockContention covers TryLock's contention contract and both lock
// variants' release paths using two open file descriptions of one file, the
// way concurrent spec subprocesses share a state file.
func TestTryLockContention(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, holder, contender *os.File)
	}{
		{
			name: "uncontended TryLock acquires and releases",
			run: func(t *testing.T, holder, contender *os.File) {
				if err := TryLock(contender.Fd()); err != nil {
					t.Fatalf("TryLock on uncontended file = %v, want nil", err)
				}
				if err := Unlock(contender.Fd()); err != nil {
					t.Fatalf("Unlock after TryLock = %v, want nil", err)
				}
			},
		},
		{
			name: "contended TryLock reports ErrLockBusy and succeeds after release",
			run: func(t *testing.T, holder, contender *os.File) {
				if err := Lock(holder.Fd()); err != nil {
					t.Fatalf("Lock = %v, want nil", err)
				}
				if err := TryLock(contender.Fd()); !errors.Is(err, ErrLockBusy) {
					t.Fatalf("TryLock while held = %v, want ErrLockBusy", err)
				}
				if err := Unlock(holder.Fd()); err != nil {
					t.Fatalf("Unlock = %v, want nil", err)
				}
				if err := TryLock(contender.Fd()); err != nil {
					t.Fatalf("TryLock after release = %v, want nil", err)
				}
				if err := Unlock(contender.Fd()); err != nil {
					t.Fatalf("Unlock after TryLock = %v, want nil", err)
				}
			},
		},
		{
			name: "blocking Lock acquires and releases a free file",
			run: func(t *testing.T, holder, contender *os.File) {
				if err := Lock(contender.Fd()); err != nil {
					t.Fatalf("Lock on free file = %v, want nil", err)
				}
				if err := Unlock(contender.Fd()); err != nil {
					t.Fatalf("Unlock after Lock = %v, want nil", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.yaml")
			holder, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
			if err != nil {
				t.Fatalf("opening lock file: %v", err)
			}
			contender, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
			if err != nil {
				t.Fatalf("opening second lock file description: %v", err)
			}
			t.Cleanup(func() {
				_ = holder.Close()
				_ = contender.Close()
			})
			tt.run(t, holder, contender)
		})
	}
}
