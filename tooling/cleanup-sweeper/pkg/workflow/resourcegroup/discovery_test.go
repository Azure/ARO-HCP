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

package resourcegroup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/sets"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

func TestDiscoverCandidates_ExplicitOnly(t *testing.T) {
	t.Parallel()

	opts := RunOptions{
		DiscoverResourceGroups: false,
		ResourceGroups:         sets.New("rg-b", "rg-a"),
	}

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	got, err := discoverCandidates(ctx, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 resource groups, got %d", len(got))
	}
	if got[0] != "rg-a" || got[1] != "rg-b" {
		t.Fatalf("expected sorted resource groups [rg-a rg-b], got %v", got)
	}
}

func TestDiscoverCandidates_RequiresReferenceTimeWhenDiscoveryEnabled(t *testing.T) {
	t.Parallel()

	opts := RunOptions{
		DiscoverResourceGroups: true,
		ResourceGroups:         sets.New[string](),
		ReferenceTime:          time.Time{},
	}

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	_, err := discoverCandidates(ctx, opts)
	if err == nil {
		t.Fatalf("expected error when reference time is zero")
	}
	if !strings.Contains(err.Error(), "reference time is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
