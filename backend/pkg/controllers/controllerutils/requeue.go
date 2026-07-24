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

import (
	"fmt"
	"time"
)

// RequeueAfterError is a sentinel error type that instructs the controller
// framework to requeue the item after a specific delay instead of using
// exponential backoff. The rate-limiter counter for the key is reset.
//
// If Err is non-nil, the framework logs it as an error for metrics/reporting.
// If Err is nil, the requeue is silent (useful for log-only outcomes).
type RequeueAfterError struct {
	Err   error
	After time.Duration
}

func (e *RequeueAfterError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("requeue after %s: %v", e.After, e.Err)
	}
	return fmt.Sprintf("requeue after %s", e.After)
}

func (e *RequeueAfterError) Unwrap() error {
	return e.Err
}
