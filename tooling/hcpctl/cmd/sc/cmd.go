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

package sc

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/base"
)

func NewCommand(group string) (*cobra.Command, error) {
	config := base.ClusterConfig{
		CommandName:         "sc",
		Aliases:             []string{"s"},
		DisplayName:         "Service Cluster",
		ShortName:           "SC",
		CompleteBreakglass:  CompleteBreakglassSC,
		CompleteList:        CompleteSCList,
		BreakglassUsageHelp: "Used for accessing service cluster infrastructure and workloads.",
		ShellMessage:        "You can now use kubectl commands to interact with the service cluster.",
	}

	return base.NewClusterCommand(config, group)
}
