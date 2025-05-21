package integration

import (
	"encoding/json"
	"os"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
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
	CustomerRGName   string                                    `json:"customer_rg_name,omitempty"`
	CustomerVNetName string                                    `json:"customer_vnet_name,omitempty"`
	CustomerNSGName  string                                    `json:"customer_nsg_name,omitempty"`
	UAMIs            generated.UserAssignedIdentitiesProfile   `json:"uamis,omitempty"`
	IdentityUAMIs    map[string]generated.UserAssignedIdentity `json:"identity_uamis,omitempty"`
}

type Cluster struct {
	Name    string                        `json:"name,omitempty"`
	ARMData generated.HcpOpenShiftCluster `json:"armdata,omitempty"`
}

type Nodepool struct {
	Name    string             `json:"name,omitempty"`
	ARMData generated.NodePool `json:"armdata,omitempty"`
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
