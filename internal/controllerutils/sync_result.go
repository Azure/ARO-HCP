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

package controllerutils

import "time"

// SyncResult carries scheduling hints from SyncOnce back to the workqueue.
// RequeueAfter is honored only when SyncOnce returns a nil error.
type SyncResult struct {
	// RequeueAfter schedules a retry via AddAfter when err is nil and RequeueAfter > 0.
	// A zero (or negative) value means no requeue is requested.
	RequeueAfter time.Duration
}

// RequeueAfterDuration returns a SyncResult that retries after d.
func RequeueAfterDuration(d time.Duration) SyncResult {
	return SyncResult{RequeueAfter: d}
}

// SyncResultFromErr returns an empty SyncResult alongside err.
func SyncResultFromErr(err error) (SyncResult, error) {
	return SyncResult{}, err
}
