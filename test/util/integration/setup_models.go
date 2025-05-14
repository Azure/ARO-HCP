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
	"os"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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
	CustomerRGName   string                             `json:"customer_rg_name,omitempty"`
	CustomerVNetName string                             `json:"customer_vnet_name,omitempty"`
	CustomerNSGName  string                             `json:"customer_nsg_name,omitempty"`
	UAMIs            api.OperatorsAuthenticationProfile `json:"uamis,omitempty"`
	IdentityUAMIs    arm.ManagedServiceIdentity         `json:"identity_uamis,omitempty"`
}

type Cluster struct {
	Name    string                  `json:"name,omitempty"`
	ARMData api.HCPOpenShiftCluster `json:"armdata,omitempty"`
}

type Nodepool struct {
	Name    string                          `json:"name,omitempty"`
	ARMData api.HCPOpenShiftClusterNodePool `json:"armdata,omitempty"`
}

func LoadE2ESetupFile(path string) (SetupModel, error) {
	e2eSetup := SetupModel{}
	content, err := os.ReadFile(path)
	if err != nil {
		return e2eSetup, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	err = decoder.Decode(&e2eSetup)
	return e2eSetup, err
}
