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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/go-github/v69/github"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
)

// newSnapshotEvidenceCommand exposes shared policy to native lifecycle consumers.
func newSnapshotEvidenceCommand() *cobra.Command {
	return &cobra.Command{Use: "snapshot-evidence", Short: "Evaluate hold and CI evidence from JSON stdin", Args: cli.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, head, err := decodeSnapshotEvidence(cmd.InOrStdin())
			if err != nil {
				return cli.Invalid(err)
			}
			result, err := orchestration.EvaluateSnapshotEvidence(snapshot, head)
			if err != nil {
				return err
			}
			record := inventoryPayload([]model.MaintenanceDecision{{ActiveHolds: result.ActiveHolds, CI: result.CI}})[0]
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				ActiveHolds         []inventoryHold `json:"active_holds"`
				CancellationPending bool            `json:"cancellation_pending"`
				TrustedHoldLabel    bool            `json:"trusted_hold_label"`
				CI                  inventoryCI     `json:"ci"`
			}{record.ActiveHolds, result.CancellationPending, result.TrustedHoldLabel, record.CI})
		}}
}

// decodeSnapshotEvidence validates required REST-shaped fields before conversion.
func decodeSnapshotEvidence(reader io.Reader) (forge.Snapshot, string, error) {
	var snapshot forge.Snapshot
	const maximum = 32 << 20
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return snapshot, "", err
	}
	if len(content) > maximum {
		return snapshot, "", errors.New("snapshot evidence is too large")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil || raw == nil {
		return snapshot, "", errors.New("snapshot evidence must be an object")
	}
	for _, name := range []string{"comments", "commits", "hold_events", "statuses", "checks"} {
		value := bytes.TrimSpace(raw[name])
		if len(value) == 0 || value[0] != '[' {
			return snapshot, "", fmt.Errorf("missing %s evidence array", name)
		}
	}
	var input struct {
		Head       string                 `json:"head"`
		Commits    []string               `json:"commits"`
		Comments   []*github.IssueComment `json:"comments"`
		HoldEvents []struct {
			ID        *int         `json:"id"`
			Event     *string      `json:"event"`
			Actor     *github.User `json:"actor"`
			CreatedAt *string      `json:"created_at"`
		} `json:"hold_events"`
		Statuses []struct {
			ID        *int    `json:"id"`
			Context   *string `json:"context"`
			State     string  `json:"state"`
			CreatedAt *string `json:"created_at"`
			TargetURL string  `json:"target_url"`
		} `json:"statuses"`
		Checks []*github.CheckRun `json:"checks"`
	}
	if err := json.Unmarshal(content, &input); err != nil {
		return snapshot, "", err
	}
	snapshot.Commits = input.Commits
	for _, comment := range input.Comments {
		if comment == nil || comment.Body == nil || comment.ID == nil || comment.CreatedAt == nil {
			return snapshot, "", errors.New("missing comment evidence")
		}
		updated := ""
		if comment.UpdatedAt != nil {
			updated = comment.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		snapshot.Comments = append(snapshot.Comments, forge.Comment{ID: int(comment.GetID()), Author: comment.GetUser().GetLogin(), Body: comment.GetBody(), CreatedAt: comment.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), UpdatedAt: updated})
	}
	for _, event := range input.HoldEvents {
		if event.ID == nil || event.Event == nil || event.Actor == nil || event.CreatedAt == nil {
			return snapshot, "", errors.New("missing hold label provenance")
		}
		snapshot.HoldEvents = append(snapshot.HoldEvents, forge.HoldEvent{ID: *event.ID, Event: *event.Event, Actor: event.Actor.GetLogin(), CreatedAt: *event.CreatedAt})
	}
	for _, status := range input.Statuses {
		if status.ID == nil || status.Context == nil || status.CreatedAt == nil {
			return snapshot, "", errors.New("missing status evidence")
		}
		snapshot.Statuses = append(snapshot.Statuses, forge.Status{ID: *status.ID, Context: *status.Context, State: status.State, CreatedAt: *status.CreatedAt, TargetURL: status.TargetURL})
	}
	for _, check := range input.Checks {
		if check == nil || check.Name == nil {
			return snapshot, "", errors.New("missing check-run evidence")
		}
		snapshot.Checks = append(snapshot.Checks, forge.Check{ID: int(check.GetID()), Name: check.GetName(), HeadSHA: check.GetHeadSHA(), Status: check.GetStatus(), Conclusion: check.GetConclusion(), DetailsURL: check.GetDetailsURL()})
	}
	return snapshot, input.Head, nil
}
