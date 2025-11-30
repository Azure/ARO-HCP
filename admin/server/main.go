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

package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/server/cmd/server"
	"github.com/Azure/ARO-HCP/admin/server/pkg/logging"
)

func main() {
	// Create the application logger
	logger := logging.New(0)
	logger.Info(fmt.Sprintf("aro-hcp-admin (%s) starting...", version()))

	cmd := &cobra.Command{
		Use:           "aro-hcp-admin",
		Short:         "Operate on ARO release artifacts.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	commands := []func() (*cobra.Command, error){
		server.NewCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			logger.Error("Failed to create subcommand.", "error", err)
			os.Exit(1)
		}
		cmd.AddCommand(c)
	}

	if err := cmd.Execute(); err != nil {
		logger.Error("Command failed.", "error", err)
		os.Exit(1)
	}
}

func version() string {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				break
			}
		}
	}

	return version
}
