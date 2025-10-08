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

package kubelogin

import (
	"github.com/spf13/cobra"

	"github.com/Azure/kubelogin/pkg/cmd"
)

func NewCommand(group string) (*cobra.Command, error) {
	// Create the kubelogin root command directly from the library
	kubeloginCmd := cmd.NewRootCmd("embedded")

	// Update the command to work as a subcommand
	kubeloginCmd.Use = "kubelogin"
	kubeloginCmd.Short = "Azure Active Directory authentication for Kubernetes"
	kubeloginCmd.Long = "Login to Azure Active Directory and populate kubeconfig with AAD tokens"
	kubeloginCmd.GroupID = group

	return kubeloginCmd, nil
}
