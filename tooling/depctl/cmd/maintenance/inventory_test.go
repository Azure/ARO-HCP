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

package maintenance

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestInventoryValidateRequiresRepository(t *testing.T) {
	_, err := (&InventoryOptions{}).Validate(context.Background())
	var exit *depctl.ExitError
	if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid {
		t.Fatalf("Validate() error = %v, want invalid input", err)
	}
}

func TestInventoryPayloadUsesTheAgentFieldNames(t *testing.T) {
	decisions := []model.MaintenanceDecision{{
		Repository: "Azure/ARO-HCP", PullRequest: 7892, ExpectedHead: "head", Head: "head",
		Kind: "tool", Held: true,
		ActiveHolds:   []model.ActiveHold{{CommentID: 7, Author: "openshift-merge-bot[bot]", Kind: "retry-exhaustion", SHA: "head"}},
		CI:            model.CIEvidence{Positive: []string{"ci/pipeline-gate"}, Blocked: []model.BlockedCheck{{Name: "ci/prow/e2e-parallel", State: "failure", URL: "https://example.invalid/run", Pending: false}}},
		ReleaseStatus: "eligible", CanReleaseHold: true, CanRecoverRetry: true,
		RetryBlockers: []string{}, RecoveryMarker: "<!-- marker -->", CanRefreshUpgrade: false,
		ReleaseBlockers: []string{}, RefreshBlockers: []string{"compiler refresh requires the owned upgrade PR identity"},
	}}
	payload := inventoryPayload(decisions)
	if len(payload) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	record := payload[0]
	for _, key := range []string{
		"repository", "pull_request", "expected_head", "head", "kind", "held", "active_holds", "ci",
		"release_status", "can_release_hold", "can_recover_retry", "retry_blockers", "recovery_marker",
		"can_refresh_upgrade", "release_blockers", "refresh_blockers",
	} {
		if _, ok := record[key]; !ok {
			t.Fatalf("record is missing %q: %+v", key, record)
		}
	}
	holds := record["active_holds"].([]map[string]any)
	if len(holds) != 1 || holds[0]["comment_id"] != 7 || holds[0]["sha"] != "head" {
		t.Fatalf("active_holds = %+v", holds)
	}
	ci := record["ci"].(map[string]any)
	blocked := ci["blocked"].([]map[string]any)
	if len(blocked) != 1 || blocked[0]["name"] != "ci/prow/e2e-parallel" {
		t.Fatalf("blocked = %+v", blocked)
	}
}
