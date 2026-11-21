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

package prioritiesgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// completed returns gate options with the network dependencies stubbed.
func completed(actor string, proposals []int, containsErr error) (*completedOptions, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &completedOptions{
		repository: "Azure/ARO-HCP", base: "main", baseCommit: "abc123",
		actor: "robot", marker: "<!-- owned -->", prefix: "bot/test-priorities-", out: out,
		actorOf:   func(context.Context) (string, error) { return actor, nil },
		contains:  func(context.Context, string, string) error { return containsErr },
		proposals: func(context.Context, string, string, string) ([]int, error) { return proposals, nil },
	}, out
}

func TestValidateRequiresEveryPrecondition(t *testing.T) {
	base := func() RawOptions {
		return RawOptions{Repository: "Azure/ARO-HCP", Base: "main", BaseCommit: "abc123", Actor: "robot", Marker: "<!-- owned -->", TokenPath: "t"}
	}
	for _, tc := range []struct {
		name string
		edit func(*RawOptions)
		want string
	}{
		{name: "repo", edit: func(r *RawOptions) { r.Repository = "" }, want: "--repo is required"},
		{name: "base", edit: func(r *RawOptions) { r.Base = "" }, want: "--base is required"},
		{name: "commit", edit: func(r *RawOptions) { r.BaseCommit = "" }, want: "--base-commit is required"},
		{name: "actor", edit: func(r *RawOptions) { r.Actor = "" }, want: "--actor is required"},
		{name: "token", edit: func(r *RawOptions) { r.TokenPath = "" }, want: "--token-path is required"},
		{name: "selector", edit: func(r *RawOptions) { r.Marker = "" }, want: "--marker or --branch-prefix is required"},
		{name: "form", edit: func(r *RawOptions) { r.Repository = "ARO-HCP" }, want: "owner/name"},
		{name: "complete", edit: func(*RawOptions) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := base()
			tc.edit(&raw)
			validated, err := raw.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if validated != nil {
				t.Fatal("a rejected invocation returned validated options")
			}
		})
	}
}

func TestRunBlocksOnAnOpenProposal(t *testing.T) {
	options, out := completed("robot", []int{9001, 9002}, nil)
	if err := options.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gate Gate
	if err := json.Unmarshal(out.Bytes(), &gate); err != nil {
		t.Fatalf("invalid gate output %q: %v", out.String(), err)
	}
	if !gate.Blocked || gate.Reason != "an open generated pull request #9001 already exists" {
		t.Fatalf("gate = %+v, want a blocked gate naming #9001", gate)
	}
}

func TestRunAllowsAnEmptyInventory(t *testing.T) {
	options, out := completed("robot", nil, nil)
	if err := options.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gate Gate
	if err := json.Unmarshal(out.Bytes(), &gate); err != nil {
		t.Fatalf("invalid gate output %q: %v", out.String(), err)
	}
	if gate.Blocked || gate.Reason != "" {
		t.Fatalf("gate = %+v, want an open gate", gate)
	}
}

func TestRunRejectsAForeignToken(t *testing.T) {
	options, _ := completed("someone-else", nil, nil)
	if err := options.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not") {
		t.Fatalf("err = %v, want a token mismatch", err)
	}
}

func TestRunRejectsABaseCommitOffTheBranch(t *testing.T) {
	options, _ := completed("robot", nil, errors.New("abc123 is not on main (compare status \"behind\")"))
	if err := options.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not on main") {
		t.Fatalf("err = %v, want a base ancestry failure", err)
	}
}
