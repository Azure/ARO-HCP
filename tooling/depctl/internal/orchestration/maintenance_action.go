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

package orchestration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// MaintenanceCommenter is the sole write capability used by hold maintenance.
type MaintenanceCommenter interface {
	PostMaintenanceComment(context.Context, int, string) (int, error)
}

// MaintenanceAction binds authorization to an explicit current head and scope.
type MaintenanceAction struct {
	Mode         string
	Repository   string
	Number       int
	Head         string
	Kind         string
	Evidence     string
	PreviousHead string
	Git          GitRunner
}

// ExecuteMaintenance refreshes complete trusted evidence twice before a write.
// All command text is fixed here; caller evidence can never inject commands.
func (e *Engine) ExecuteMaintenance(ctx context.Context, action MaintenanceAction, writer MaintenanceCommenter) (model.MaintenanceDecision, int, error) {
	var decision model.MaintenanceDecision
	if err := policy.ValidateMaintenanceTarget(action.Repository, action.Number, action.Head); err != nil {
		return decision, 0, err
	}
	kinds, err := policy.MaintenanceKinds(action.Kind)
	if err != nil {
		return decision, 0, err
	}
	reads := 2
	switch action.Mode {
	case "assess", "verify-upgrade":
		reads = 1
	case "release-hold", "recover-retry", "release-regenerated":
	default:
		return decision, 0, errors.New("unsupported maintenance action")
	}
	if e.deps.Forge == nil {
		return decision, 0, ErrNoForge
	}
	body := ""
	for range reads {
		snapshot, err := e.deps.Forge.Snapshot(ctx, action.Number, action.Head)
		if err != nil {
			return decision, 0, err
		}
		decision, err = policy.Assess(action.Repository, action.Number, action.Head, snapshot)
		if err != nil {
			return decision, 0, err
		}
		if action.Kind != "" && !slices.Contains(kinds, decision.Kind) {
			return decision, 0, errors.New("pull request kind does not match requested scope")
		}
		if action.Mode == "assess" {
			return decision, 0, nil
		}
		if action.Mode == "verify-upgrade" {
			if len(decision.RefreshBlockers) > 0 {
				return decision, 0, errors.New(strings.Join(decision.RefreshBlockers, "; "))
			}
			return decision, 0, nil
		}
		if !decision.Held || decision.ReleaseStatus == policy.ReleaseCancelPend {
			return decision, 0, nil
		}
		switch action.Mode {
		case "release-hold":
			if len(decision.ReleaseBlockers) > 0 {
				return decision, 0, errors.New(strings.Join(decision.ReleaseBlockers, "; "))
			}
			body = "/hold cancel"
		case "recover-retry":
			if len(decision.RetryBlockers) > 0 {
				return decision, 0, errors.New(strings.Join(decision.RetryBlockers, "; "))
			}
			if err := policy.RecoveryEvidence(action.Evidence, decision); err != nil {
				return decision, 0, err
			}
			body = decision.RecoveryMarker + "\n" + action.Evidence + "\n\n/hold cancel\n/retest-required"
		case "release-regenerated":
			if len(decision.RefreshBlockers) > 0 {
				return decision, 0, errors.New(strings.Join(decision.RefreshBlockers, "; "))
			}
			if err := policy.ValidateMaintenanceTarget(action.Repository, action.Number, action.PreviousHead); err != nil {
				return decision, 0, err
			}
			for _, hold := range decision.ActiveHolds {
				if hold.SHA == action.Head {
					return decision, 0, errors.New("regeneration must produce a genuinely new head")
				}
			}
			run := action.Git
			if run == nil {
				run = RunGit
			}
			if err := ValidateRegeneration(ctx, action.PreviousHead, action.Head, snapshot.Pull.BaseRef, run); err != nil {
				return decision, 0, err
			}
			body = fmt.Sprintf("Validated compiler regeneration from %s to %s.\n\n/hold cancel", action.PreviousHead, action.Head)
		}
	}
	if writer == nil {
		return decision, 0, errors.New("maintenance comment writer is unavailable")
	}
	id, err := writer.PostMaintenanceComment(ctx, action.Number, body)
	if err != nil {
		return decision, 0, err
	}
	if id <= 0 {
		return decision, 0, errors.New("hold cancellation response is incomplete; inspect the PR before retrying")
	}
	decision.ReleaseStatus = "cancel-requested"
	decision.CanReleaseHold = false
	return decision, id, nil
}

// SnapshotEvidence is the native consumer's shared hold and CI policy result.
type SnapshotEvidence struct {
	ActiveHolds         []model.ActiveHold
	CancellationPending bool
	TrustedHoldLabel    bool
	CI                  model.CIEvidence
}

// EvaluateSnapshotEvidence evaluates the same policy used for remote snapshots.
func EvaluateSnapshotEvidence(snapshot forge.Snapshot, head string) (SnapshotEvidence, error) {
	var result SnapshotEvidence
	if err := policy.ValidateMaintenanceTarget("owner/repo", 1, head); err != nil {
		return result, err
	}
	commits := map[string]bool{}
	for _, sha := range snapshot.Commits {
		if err := policy.ValidateMaintenanceTarget("owner/repo", 1, sha); err != nil {
			return result, err
		}
		commits[sha] = true
	}
	var err error
	result.ActiveHolds, result.CancellationPending, err = policy.ActiveHolds(snapshot.Comments, commits)
	if err != nil {
		return result, err
	}
	result.TrustedHoldLabel, err = policy.TrustedHoldLabel(snapshot.HoldEvents)
	if err != nil {
		return result, err
	}
	result.CI, err = policy.CIEvidence(snapshot.Statuses, snapshot.Checks, head)
	return result, err
}
