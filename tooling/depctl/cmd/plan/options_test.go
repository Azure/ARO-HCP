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

package plan

import (
	"errors"
	"io"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestPlanOutcomeReportsBlockersBeforeNoChange(t *testing.T) {
	blocked := model.Plan{Outcome: model.OutcomeNoChange, Blockers: []string{"inventory is incomplete"}}
	err := planOutcome(blocked, io.Discard)
	var exit *depctl.ExitError
	if !errors.As(err, &exit) || exit.Code != depctl.ExitIncomplete {
		t.Fatalf("planOutcome() error = %v, want exit code %d", err, depctl.ExitIncomplete)
	}
	if err := planOutcome(model.Plan{Outcome: model.OutcomeNoChange}, io.Discard); err != nil {
		t.Fatalf("planOutcome() error = %v for a blocker-free no-change", err)
	}
	if err := planOutcome(model.Plan{Outcome: model.OutcomePrepared}, io.Discard); err != nil {
		t.Fatalf("planOutcome() error = %v for a prepared plan", err)
	}
}
