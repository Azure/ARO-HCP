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

// Command depctl runs the ARO-HCP dependency automation lifecycle locally.
//
// The commands are thin adapters over reusable workflow code. Only commands whose
// guards exist are registered: no live publication, remote mutation, or scheduled
// execution path is reachable from this binary.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Azure/ARO-HCP/tooling/depctl/cmd"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// Runs the CLI and exits with its documented code.
func main() {
	if err := cmd.Execute(); err != nil {
		var exit *depctl.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, exit.Message)
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(depctl.ExitInternal)
	}
}
