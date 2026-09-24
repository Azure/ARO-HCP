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

package resourcegroups

import "testing"

func TestDefaultOptionsUseBoundedConcurrency(t *testing.T) {
	t.Parallel()

	if got := DefaultOptions().Concurrency; got != DefaultConcurrency {
		t.Fatalf("expected default concurrency %d, got %d", DefaultConcurrency, got)
	}
}

func TestValidateRejectsInvalidConcurrency(t *testing.T) {
	t.Parallel()

	options := DefaultOptions()
	options.DeleteExpired = true
	options.Concurrency = 0

	if _, err := options.Validate(); err == nil {
		t.Fatal("expected zero concurrency to be rejected")
	}
}
