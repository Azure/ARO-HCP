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

package cli

import (
	"errors"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestMapEngineErrorUsesTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{name: "stale base", err: orchestration.ErrStale, code: depctl.ExitStale},
		{name: "candidate drift", err: orchestration.ErrCandidateDrift, code: depctl.ExitStale},
		{name: "candidate mismatch", err: workspace.ErrCandidateMismatch, code: depctl.ExitStale},
		{name: "plan rejected", err: orchestration.ErrPlanRejected, code: depctl.ExitIncomplete},
		{name: "generation failed", err: orchestration.ErrGeneration, code: depctl.ExitValidation},
		{name: "unclassified failure", err: errors.New("write candidate patch: no space left on device"), code: depctl.ExitInternal},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var exit *depctl.ExitError
			if !errors.As(MapEngineError(testCase.err), &exit) {
				t.Fatalf("MapEngineError() did not return an exit error")
			}
			if exit.Code != testCase.code {
				t.Fatalf("code = %d, want %d", exit.Code, testCase.code)
			}
		})
	}
}
