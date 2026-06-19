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
		// upgrade/post-infra loads the JSON written by upgrade/create. openshift/release wires
		// SETUP_FILEPATH=${SHARED_DIR}/e2e-setup.json on both aro-hcp-test-local-pre-upgrade and
		// aro-hcp-test-local-post-upgrade so the file survives git-checkout-head between steps.
		e2eSetup, err = integration.LoadE2ESetupFile(integration.E2ESetupFilePath())
		if err != nil {
			return fmt.Errorf("failed to load e2e setup file: %w", err)
		}
	}

	return nil
}
