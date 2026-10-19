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

package infrastructureidentities

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

const (
	defaultManagementStampCount = 2
	serviceResourceGroupSuffix  = "-svc"
	managementResourceGroupBase = "-mgmt-"
	maxAzureResourceGroupLength = 90
)

// Topology is the persistent identity set required by one ephemeral service
// cluster and its management stamps. Names match the product templates in
// svc-cluster.bicep and mgmt-cluster.bicep, plus image-puller and the leased
// AKS control-plane identities. The count is derived from this set; it is not
// a universal constant.
type Topology struct {
	ManagementStampCount      int
	ServiceAKSIdentityName    string
	ManagementAKSIdentityName string
	ServiceIdentityNames      []string
	ManagementIdentityNames   []string
}

func DefaultTopology() Topology {
	return Topology{
		ManagementStampCount:      defaultManagementStampCount,
		ServiceAKSIdentityName:    "service-control-plane",
		ManagementAKSIdentityName: "management-control-plane",
		ServiceIdentityNames: []string{
			"admin-api",
			"aro-billing",
			"aro-hcp-exporter",
			"backend",
			"clusters-service",
			"fleet",
			"frontend",
			"image-puller",
			"logs-mdsd",
			"maestro-server",
			"msi-credential-refresher",
			"prometheus",
			"service-control-plane",
			"sessiongate",
		},
		ManagementIdentityNames: []string{
			"image-puller",
			"kube-applier",
			"logs-mdsd",
			"maestro-consumer",
			"management-control-plane",
			"mgmt-agent",
			"prometheus",
			"velero",
		},
	}
}

type azureResourceGroup struct {
	Name           string
	IdentityNames  []string
	ServiceCluster bool
	Stamp          int
}

func (t Topology) AzureResourceGroups(leaseName string) ([]azureResourceGroup, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	groups := []azureResourceGroup{{
		Name:           leaseName + serviceResourceGroupSuffix,
		IdentityNames:  slices.Clone(t.ServiceIdentityNames),
		ServiceCluster: true,
	}}
	for stamp := 1; stamp <= t.ManagementStampCount; stamp++ {
		groups = append(groups, azureResourceGroup{
			Name:          leaseName + managementResourceGroupBase + strconv.Itoa(stamp),
			IdentityNames: slices.Clone(t.ManagementIdentityNames),
			Stamp:         stamp,
		})
	}
	for _, group := range groups {
		if len(group.Name) > maxAzureResourceGroupLength {
			return nil, fmt.Errorf("derived resource group %q exceeds the 90-character Azure limit", group.Name)
		}
	}
	return groups, nil
}

func (t Topology) Bundle(leaseName string) (leaseenv.InfrastructureIdentityBundlePayload, error) {
	groups, err := t.AzureResourceGroups(leaseName)
	if err != nil {
		return leaseenv.InfrastructureIdentityBundlePayload{}, err
	}
	bundle := leaseenv.InfrastructureIdentityBundlePayload{
		ServiceAksIdentityName:     t.ServiceAKSIdentityName,
		ManagementResourceGroups:   map[string]string{},
		ManagementAksIdentityNames: map[string]string{},
	}
	for _, group := range groups {
		if group.ServiceCluster {
			bundle.ServiceResourceGroup = group.Name
			continue
		}
		stamp := strconv.Itoa(group.Stamp)
		bundle.ManagementResourceGroups[stamp] = group.Name
		bundle.ManagementAksIdentityNames[stamp] = t.ManagementAKSIdentityName
	}
	return bundle, nil
}

func (t Topology) IdentityInventory() []string {
	names := slices.Clone(t.ServiceIdentityNames)
	for range t.ManagementStampCount {
		names = append(names, t.ManagementIdentityNames...)
	}
	return names
}

func (t Topology) validate() error {
	switch {
	case t.ManagementStampCount < 1:
		return fmt.Errorf("management stamp count must be at least 1, got %d", t.ManagementStampCount)
	case t.ServiceAKSIdentityName == "":
		return fmt.Errorf("service AKS identity name is empty")
	case t.ManagementAKSIdentityName == "":
		return fmt.Errorf("management AKS identity name is empty")
	case len(t.ServiceIdentityNames) == 0:
		return fmt.Errorf("service identity set is empty")
	case len(t.ManagementIdentityNames) == 0:
		return fmt.Errorf("management identity set is empty")
	case !slices.Contains(t.ServiceIdentityNames, t.ServiceAKSIdentityName):
		return fmt.Errorf("service AKS identity %q is not in the service identity set", t.ServiceAKSIdentityName)
	case !slices.Contains(t.ManagementIdentityNames, t.ManagementAKSIdentityName):
		return fmt.Errorf("management AKS identity %q is not in the management identity set", t.ManagementAKSIdentityName)
	}
	return nil
}
