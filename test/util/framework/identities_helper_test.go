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

package framework

import (
	"fmt"
	"testing"
)

func TestRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		initialState       leaseState
		initialLeasedBy    string
		cleanups           []func() error
		wantErr            bool
		wantState          leaseState
		wantLeasedBy       string
		wantHistoryLen     int
		wantCleanupsCalled int
	}{
		{
			name:               "free entry is a no-op",
			initialState:       leaseStateFree,
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     0,
			wantCleanupsCalled: 0,
		},
		{
			name:               "assigned entry skips cleanups",
			initialState:       leaseStateAssigned,
			initialLeasedBy:    "test-spec-1",
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     1,
			wantCleanupsCalled: 0,
		},
		{
			name:               "busy entry runs cleanups",
			initialState:       leaseStateBusy,
			initialLeasedBy:    "test-spec-1",
			wantErr:            false,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     1,
			wantCleanupsCalled: 2,
		},
		{
			name:            "busy entry with failing cleanup returns error",
			initialState:    leaseStateBusy,
			initialLeasedBy: "test-spec-1",
			cleanups: []func() error{
				func() error { return nil },
				func() error { return fmt.Errorf("cleanup failed") },
			},
			wantErr:            true,
			wantState:          leaseStateFree,
			wantLeasedBy:       "",
			wantHistoryLen:     1,
			wantCleanupsCalled: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cleanupsCalled := 0
			entry := &leasedIdentityPoolEntry{
				ResourceGroup: "rg-test",
				Current: leaseEntry{
					State:    tc.initialState,
					LeasedBy: tc.initialLeasedBy,
				},
			}

			cleanups := tc.cleanups
			if cleanups == nil && tc.wantCleanupsCalled > 0 {
				for range tc.wantCleanupsCalled {
					cleanups = append(cleanups, func() error {
						cleanupsCalled++
						return nil
					})
				}
			} else if cleanups != nil {
				origCleanups := cleanups
				cleanups = make([]func() error, len(origCleanups))
				for i, fn := range origCleanups {
					cleanups[i] = func() error {
						cleanupsCalled++
						return fn()
					}
				}
			}

			err := entry.release(cleanups...)

			if (err != nil) != tc.wantErr {
				t.Errorf("release() error = %v, wantErr %v", err, tc.wantErr)
			}
			if entry.Current.State != tc.wantState {
				t.Errorf("release() state = %q, want %q", entry.Current.State, tc.wantState)
			}
			if entry.Current.LeasedBy != tc.wantLeasedBy {
				t.Errorf("release() leasedBy = %q, want %q", entry.Current.LeasedBy, tc.wantLeasedBy)
			}
			if len(entry.History) != tc.wantHistoryLen {
				t.Errorf("release() history length = %d, want %d", len(entry.History), tc.wantHistoryLen)
			}
			if cleanupsCalled != tc.wantCleanupsCalled {
				t.Errorf("release() cleanups called = %d, want %d", cleanupsCalled, tc.wantCleanupsCalled)
			}
		})
	}
}
