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
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/pkg/prometheusrules"
)

func main() {
	if os.Getenv("DEBUG") == "true" {
		logrus.SetLevel(logrus.DebugLevel)
	}

	cmd, err := newCommand()
	if err != nil {
		logrus.WithError(err).Fatal("failed to create command")
	}

	if err := cmd.Execute(); err != nil {
		logrus.WithError(err).Fatal("error running generator")
	}
}

func newCommand() (*cobra.Command, error) {
	opts := prometheusrules.DefaultOptions()
	cmd := &cobra.Command{
		Use:           "prometheus-rules",
		Short:         "Generate Azure Bicep templates from Prometheus rules",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(opts)
		},
	}
	if err := prometheusrules.BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func run(opts *prometheusrules.RawOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.Run()
}
