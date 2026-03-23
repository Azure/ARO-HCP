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

package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

func TestRun_ReturnsStepDiscoveryError(t *testing.T) {
	t.Parallel()

	ctx := cleanuprunner.ContextWithLogger(context.Background(), logr.Discard())
	err := Run(ctx, RunOptions{
		SubscriptionID:  "00000000-0000-0000-0000-000000000000",
		AzureCredential: nil,
	})
	if err == nil {
		t.Fatalf("expected error when credential is nil")
	}
	if !strings.Contains(err.Error(), "azure credential is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
