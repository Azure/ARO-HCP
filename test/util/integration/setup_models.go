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

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type SetupModel struct {
	E2ESetup    E2ESetup    `json:"e2e_setup"`
	CustomerEnv CustomerEnv `json:"customer_env"`
	Cluster     Cluster     `json:"cluster"`
	Nodepools   []Nodepool  `json:"nodepools"`
}

type E2ESetup struct {
	Name string   `json:"name"`
	Tags []string `json:"tags,omitempty"`
}

type CustomerEnv struct {
	CustomerRGName   string                                              `json:"customer_rg_name,omitempty"`
	CustomerVNetName string                                              `json:"customer_vnet_name,omitempty"`
	CustomerNSGName  string                                              `json:"customer_nsg_name,omitempty"`
	UAMIs            hcpsdk20240610preview.UserAssignedIdentitiesProfile `json:"uamis,omitempty"`
	IdentityUAMIs    hcpsdk20240610preview.ManagedServiceIdentity        `json:"identity_uamis,omitempty"`
}

type Cluster struct {
	Name    string                                    `json:"name,omitempty"`
	ARMData hcpsdk20240610preview.HcpOpenShiftCluster `json:"armdata,omitempty"`
}

type Nodepool struct {
	Name    string                         `json:"name,omitempty"`
	ARMData hcpsdk20240610preview.NodePool `json:"armdata,omitempty"`
	Hash    string                         `json:"hash,omitempty"`
}

// E2ESetupFilePath returns the path used for reading and writing e2e-setup.json.
// SETUP_FILEPATH overrides the local default (test-artifacts/e2e-setup.json). Multi-step
// CI jobs (openshift/release aro-hcp-local-e2e-upgrade) set it to ${SHARED_DIR}/e2e-setup.json
// so upgrade/create output is consumed by upgrade/post-infra across Prow steps. The parent
// directory must already exist (e.g. mkdir test-artifacts locally, or use SHARED_DIR in CI).
func E2ESetupFilePath() string {
	if path := os.Getenv("SETUP_FILEPATH"); path != "" {
		return path
	}
	return filepath.Join("test-artifacts", "e2e-setup.json")
}

func LoadE2ESetupFile(path string) (SetupModel, error) {
	e2eSetup := SetupModel{}
	content, err := os.ReadFile(path)
	if err != nil {
		return e2eSetup, err
	}
	err = json.Unmarshal(content, &e2eSetup)
	return e2eSetup, err
}

func WriteE2ESetupFile(content []byte) error {
	path := E2ESetupFilePath()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write e2e setup file %q: %w", path, err)
	}
	return nil
}
