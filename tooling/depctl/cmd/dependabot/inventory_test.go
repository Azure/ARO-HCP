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

package dependabot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestInventoryValidateRequiresRepository(t *testing.T) {
	_, err := (&InventoryOptions{}).Validate(context.Background())
	var exit *depctl.ExitError
	if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid {
		t.Fatalf("Validate() error = %v, want invalid input", err)
	}
}

func TestInventoryRunEmitsSecurityRecords(t *testing.T) {
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 7, Author: policy.Robot, State: "open",
		Title: "fix(deps): yq to 4.54.3", Body: policy.SecurityWorkflowMarker,
		HeadRef: "branch", HeadSHA: "39af3a51dca8b0507907cd9f00c2d33ad659e227",
		HeadRepo: "Azure/ARO-HCP", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
	}, forge.Snapshot{})
	previous := readerFactory
	readerFactory = func(string) (forge.Reader, error) { return fake, nil }
	t.Cleanup(func() { readerFactory = previous })

	validated, err := (&InventoryOptions{Repository: "Azure/ARO-HCP"}).Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	var out, errOut bytes.Buffer
	completed.out, completed.err = &out, &errOut
	if err := completed.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var records []struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		HeadRepo string `json:"head_repo"`
		Base     string `json:"base"`
		SHA      string `json:"sha"`
		Author   string `json:"author"`
	}
	if err := json.Unmarshal(out.Bytes(), &records); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(records) != 1 || records[0].Number != 7 || records[0].Base != "main" ||
		records[0].HeadRepo != "Azure/ARO-HCP" || records[0].Author != policy.Robot {
		t.Fatalf("records = %+v", records)
	}
}
