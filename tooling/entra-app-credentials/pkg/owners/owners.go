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

// Package owners checks the configured Entra application owner IDs against
// the live membership of the Entra group they are meant to mirror.
package owners

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Member is a user or service principal that can own an Entra application.
type Member struct {
	ID          string
	DisplayName string
}

// Object is a directory object looked up by ID.
type Object struct {
	DisplayName string
	// Deleted is true when the object is soft-deleted (in the Entra recycle bin).
	Deleted bool
}

// Directory reads the Entra directory.
type Directory interface {
	// GroupMembers returns the users and service principals in the group,
	// including those that are members through nested groups.
	GroupMembers(ctx context.Context, groupID string) ([]Member, error)
	// Lookup returns the directory object with the given ID, or nil when no
	// such object exists, deleted or not.
	Lookup(ctx context.Context, id string) (*Object, error)
}

// Stale is a configured owner that must be removed from the configuration.
type Stale struct {
	ID     string
	Reason string
}

// Drift is a mismatch between the configured owner IDs and the group members.
type Drift struct {
	GroupID string
	// Stale lists configured IDs that are not group members.
	Stale []Stale
	// Missing lists group members absent from the configuration.
	Missing []Member
}

func (d *Drift) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "configured owner IDs do not match the members of Entra group %s.\n", d.GroupID)
	if len(d.Stale) > 0 {
		b.WriteString("\nRemove these IDs from entraAppOwnerIds (Microsoft Graph rejects the owner update for IDs that do not exist):\n")
		for _, stale := range d.Stale {
			fmt.Fprintf(&b, "  %s  %s\n", stale.ID, stale.Reason)
		}
	}
	if len(d.Missing) > 0 {
		b.WriteString("\nAdd these group members to entraAppOwnerIds (the list is authoritative, so they would otherwise be removed as owners and lose access):\n")
		for _, member := range d.Missing {
			fmt.Fprintf(&b, "  %s  %s\n", member.ID, member.DisplayName)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ParseIDs splits a comma-separated list of object IDs, normalizing them to
// lower case. It rejects empty lists, malformed IDs and duplicates.
func ParseIDs(csv string) ([]string, error) {
	var ids []string
	for _, raw := range strings.Split(csv, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, err := uuid.Parse(raw); err != nil {
			return nil, fmt.Errorf("owner ID %q is not a valid object ID", raw)
		}
		id := strings.ToLower(raw)
		if slices.Contains(ids, id) {
			return nil, fmt.Errorf("owner ID %q is listed more than once", id)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no owner IDs configured")
	}
	return ids, nil
}

// Check returns a *Drift error when configured differs from the members of the
// group, and nil when they match.
func Check(ctx context.Context, directory Directory, groupID string, configured []string) error {
	members, err := directory.GroupMembers(ctx, groupID)
	if err != nil {
		return fmt.Errorf("list members of group %s: %w", groupID, err)
	}

	memberIDs := make(map[string]bool, len(members))
	for _, member := range members {
		memberIDs[strings.ToLower(member.ID)] = true
	}

	drift := &Drift{GroupID: groupID}
	configuredIDs := make(map[string]bool, len(configured))
	for _, id := range configured {
		id = strings.ToLower(id)
		configuredIDs[id] = true
		if memberIDs[id] {
			continue
		}
		reason, err := staleReason(ctx, directory, id)
		if err != nil {
			return err
		}
		drift.Stale = append(drift.Stale, Stale{ID: id, Reason: reason})
	}
	for _, member := range members {
		if !configuredIDs[strings.ToLower(member.ID)] {
			drift.Missing = append(drift.Missing, member)
		}
	}

	if len(drift.Stale) == 0 && len(drift.Missing) == 0 {
		return nil
	}
	slices.SortFunc(drift.Stale, func(a, b Stale) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(drift.Missing, func(a, b Member) int {
		return strings.Compare(strings.ToLower(a.DisplayName)+a.ID, strings.ToLower(b.DisplayName)+b.ID)
	})
	return drift
}

func staleReason(ctx context.Context, directory Directory, id string) (string, error) {
	object, err := directory.Lookup(ctx, id)
	if err != nil {
		return "", fmt.Errorf("look up configured owner %s: %w", id, err)
	}
	switch {
	case object == nil:
		return "does not exist in the tenant", nil
	case object.Deleted:
		return fmt.Sprintf("deleted account %q (in the Entra recycle bin)", object.DisplayName), nil
	default:
		return fmt.Sprintf("%q is not a member of the group", object.DisplayName), nil
	}
}
