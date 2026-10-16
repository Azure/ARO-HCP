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

package cmd

import (
	"os"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
)

// runCapture enters the namespace at path and writes its rtnetlink state as
// JSON to stdout. It backs the hidden "capture" helper subcommand, which is
// only ever exec'd as a dedicated child process on the same Linux node.
func runCapture(namespacePath string) error {
	return capture.Run(namespacePath, os.Stdout)
}
