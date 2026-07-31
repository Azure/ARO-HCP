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

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"sigs.k8s.io/yaml"

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
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.CorrelationMap {
				configs := args
				if opts.ConfigFile != "" {
					configs = append([]string{opts.ConfigFile}, configs...)
				}
				if len(configs) == 0 {
					return fmt.Errorf("at least one config file must be provided via --config-file or as arguments")
				}
				entries, err := prometheusrules.GenerateCorrelationMap(configs)
				if err != nil {
					return fmt.Errorf("error generating correlation map: %w", err)
				}
				out, err := yaml.Marshal(entries)
				if err != nil {
					return fmt.Errorf("error marshaling correlation map: %w", err)
				}
				if _, err := os.Stdout.Write(out); err != nil {
					return fmt.Errorf("error writing correlation map: %w", err)
				}
				return nil
			}

			if len(args) > 0 {
				return fmt.Errorf("unexpected positional arguments: %v", args)
			}
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
