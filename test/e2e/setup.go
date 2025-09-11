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

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var (
	e2eSetup integration.SetupModel
)

func Setup() error {
	// Use GinkgoLabelFilter to check for the 'requirenothing' label
	if strings.Contains(GinkgoLabelFilter(), labels.RequireNothing[0]) {
		// Skip loading the e2esetup file
		e2eSetup = integration.SetupModel{} // zero value
	} else {
		var err error
		// Load pre created cluster setup file for per test cluster tests
		setupFilePath := os.Getenv("SETUP_FILEPATH")
		if setupFilePath == "" {
			setupFilePath = filepath.Join("test", "e2e", "test-artifacts", "e2e-setup.json")
		}
		e2eSetup, err = integration.LoadE2ESetupFile(setupFilePath)
		if err != nil {
			return fmt.Errorf("failed to load e2e setup file: %w", err)
		}
	}

	return nil
}
