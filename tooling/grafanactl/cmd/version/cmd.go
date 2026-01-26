// Copyright 2025 Microsoft Corporation
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

package version

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/pkg/version"
)

func NewCommand(group string) (*cobra.Command, error) {
	return &cobra.Command{
		Use:     "version",
		Short:   "Display version information",
		Long:    "Display the commit SHA and build date of grafanactl.",
		GroupID: group,
		Run: func(cmd *cobra.Command, args []string) {
			versionInfo := version.GetVersionInfo()
			fmt.Printf("Commit SHA: %s\n", versionInfo.Commit)
			fmt.Printf("Build Date: %s\n", versionInfo.BuildDate)
		},
	}, nil
}
