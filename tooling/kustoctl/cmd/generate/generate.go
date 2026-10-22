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

// Package generate implements "kustoctl generate", which renders generated
// artifacts from committed templates. These outputs must be committed
// alongside their templates and are checked for drift by CI (see
// hack/verify.sh / `make verify-generate`), following this repo's existing
// generated-artifact convention (deepcopy, mocks, Helm materialize fixtures,
// bicep golden fixtures): a pure function of committed inputs, rendered and
// committed, with CI failing on any drift between the two.
package generate

import (
	"github.com/spf13/cobra"
)

// NewGenerateCommand returns the "generate" command and its subcommands.
func NewGenerateCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Render generated artifacts from committed templates",
		Long: `generate renders generated artifacts (e.g. main.bicep) from their
committed templates. Generated output must be committed and is expected to be
regenerated via "make generate" whenever its inputs change; CI's
"make verify-generate" fails the build if committed output has drifted from a
fresh render.`,
	}

	cmd.AddCommand(newKQLBicepCommand())

	return cmd, nil
}
