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

package deletestack

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the delete command.
func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a deployment stack, detaching or deleting the resources it manages",
		Long: `Deletes the named deployment stack from a resource group. --action-on-unmanage is required and decides what
happens to every resource, resource group and management group the stack manages: "detach" leaves them in place,
"delete" deletes them. A stack or resource group that does not exist is not an error, so the command is safe to
run repeatedly.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
