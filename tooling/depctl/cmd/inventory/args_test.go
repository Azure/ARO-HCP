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

package inventory

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestNoArgsReportsInvalidInput(t *testing.T) {
	err := cli.NoArgs(&cobra.Command{Use: "x"}, []string{"unexpected"})
	var exit *depctl.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("NoArgs() did not return an exit error")
	}
	if exit.Code != depctl.ExitInvalid {
		t.Fatalf("code = %d, want %d", exit.Code, depctl.ExitInvalid)
	}
	if err := cli.NoArgs(&cobra.Command{Use: "x"}, nil); err != nil {
		t.Fatalf("NoArgs() with no arguments must pass: %v", err)
	}
}
