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

package prometheusrules

import (
	"errors"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/internal"
)

// Validate checks CLI arguments for rule generation.
func Validate(args []string, configFilePath, promtoolPath string) error {
	if len(args) != 0 {
		return errors.New("no arguments are supported")
	}
	if configFilePath == "" {
		return errors.New("--config-file is required")
	}
	if promtoolPath == "" {
		return errors.New("--promtool-path cannot be empty")
	}
	return nil
}

// GenerateFromConfig validates and renders rule files into Bicep output.
func GenerateFromConfig(configFilePath string, promtoolPath string) error {
	o := internal.NewOptions()

	if err := o.Complete(configFilePath, promtoolPath); err != nil {
		return fmt.Errorf("could not complete options, %w", err)
	}
	if err := o.RunTests(); err != nil {
		return fmt.Errorf("testing rules failed %w", err)
	}
	if err := o.Generate(); err != nil {
		return fmt.Errorf("failed to generate bicep %w", err)
	}

	return nil
}
