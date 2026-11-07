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

package providers

import (
	"strings"
	"testing"
)

func TestPickSoleEnabledSubscription(t *testing.T) {
	t.Parallel()

	t.Run("exactly one", func(t *testing.T) {
		t.Parallel()
		got, err := pickSoleEnabledSubscription([]string{"sub-a"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "sub-a" {
			t.Fatalf("got %q, want sub-a", got)
		}
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		_, err := pickSoleEnabledSubscription(nil)
		if err == nil || !strings.Contains(err.Error(), "no Enabled") {
			t.Fatalf("expected empty-list error, got %v", err)
		}
	})

	t.Run("multiple", func(t *testing.T) {
		t.Parallel()
		_, err := pickSoleEnabledSubscription([]string{"a", "b"})
		if err == nil || !strings.Contains(err.Error(), "2 Enabled") {
			t.Fatalf("expected multi-sub error, got %v", err)
		}
	})
}
