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

package evidence

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestValidateRejectsAMalformedURLAsInvalidInput(t *testing.T) {
	options := &RawOptions{
		ProwURL:   "https://example.com/not-a-prow-run",
		OutputDir: t.TempDir(),
	}
	_, err := options.Validate(context.Background())
	var exit *depctl.ExitError
	if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid {
		t.Fatalf("Validate() error = %v, want exit code %d", err, depctl.ExitInvalid)
	}
}

func TestValidateAcceptsACanonicalProwURL(t *testing.T) {
	options := &RawOptions{
		ProwURL: "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/" +
			"pull/Azure_ARO-HCP/7998/pull-ci-Azure-ARO-HCP-main-verify/2120161093362913280",
		OutputDir: t.TempDir(),
	}
	if _, err := options.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
