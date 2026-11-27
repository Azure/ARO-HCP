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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	v20261001 "github.com/Azure/ARO-HCP/internal/azureapi/v20261001"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001/generated"
)

const (
	openShiftMinorVersion = "4.20"
	subscription          = "/subscriptions/00000000-0000-0000-0000-000000000000"
	network               = subscription + "/resourceGroups/dev-test-rg/providers/Microsoft.Network"
	subnets               = network + "/virtualNetworks/xyz/subnets/"
)

var defaultAPIVersion = metadataapi.APIVersionV20261001

func main() {
	registry := coreapi.NewAPIRegistry()
	metadataapi.Must[any](nil, v20240610preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20251223preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20260630preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20260901preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20261001.RegisterVersion(registry))

	supported := strings.Join(registry.ListVersions().SortedList(), ", ")
	apiVersion := flag.String("api-version", string(defaultAPIVersion), "ARM API version ("+supported+")")
	objectType := flag.String("type", "cluster", "resource to generate (cluster or node_pool)")
	flag.Parse()

	version, ok := registry.Lookup(*apiVersion)
	if !ok {
		fmt.Fprintf(os.Stderr, "unsupported API version %q (choose from %s)\n", *apiVersion, supported)
		os.Exit(2)
	}
	if err := writeRequest(*objectType, version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeRequest(objectType string, version coreapi.Version) error {
	var resource any
	var err error
	switch objectType {
	case "cluster":
		resource, err = clusterRequest(version)
	case "node_pool":
		resource, err = nodePoolRequest(version)
	default:
		return fmt.Errorf("unsupported resource type %q (choose cluster or node_pool)", objectType)
	}
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(objectType+".json", append(data, '\n'), 0644)
}

// Define the sample once and use the frontend's conversions for each ARM schema.
func clusterRequest(version coreapi.Version) (coreapi.VersionedCluster, error) {
	sample := &v20261001.HcpOpenShiftCluster{}
	sample.Location = ptr.To("westus3")
	sample.Properties = &generated.HcpOpenShiftClusterProperties{
		Version: &generated.VersionProfile{ID: ptr.To(openShiftMinorVersion)},
		Platform: &generated.PlatformProfile{
			ManagedResourceGroup:    ptr.To("dev-test-mrg"),
			SubnetID:                ptr.To(subnets + "xyz"),
			VnetIntegrationSubnetID: ptr.To(subnets + "integration"),
			NetworkSecurityGroupID:  ptr.To(network + "/networkSecurityGroups/xyz"),
		},
		Etcd: &generated.EtcdProfile{DataEncryption: &generated.EtcdDataEncryptionProfile{
			KeyManagementMode: ptr.To(generated.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged),
			CustomerManaged: &generated.CustomerManagedEncryptionProfile{
				EncryptionType: ptr.To(generated.CustomerManagedEncryptionTypeKms),
				Kms: &generated.KmsEncryptionProfile{
					VaultName:  ptr.To("dev-test-vault"),
					Visibility: ptr.To(generated.KeyVaultVisibilityPublic),
					ActiveKey:  &generated.KmsKey{Name: ptr.To("etcd"), Version: ptr.To("4e832b5c8f1e4e3d9c6b2a1f3e7d9c5b")},
				},
			},
		}},
	}
	sample.Identity, sample.Properties.Platform.OperatorsAuthentication = operatorIdentities()
	v20261001.SetDefaultValuesCluster(sample)
	cluster, err := sample.ConvertToInternal(nil)
	if err != nil {
		return nil, err
	}
	request := version.NewCluster(cluster)
	request.ClearReadOnlyFields()
	return request, nil
}

func operatorIdentities() (*generated.ManagedServiceIdentity, *generated.OperatorsAuthenticationProfile) {
	const prefix = subscription + "/resourceGroups/dev-test-identities/providers/Microsoft.ManagedIdentity/userAssignedIdentities/"
	config := azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
	operators := &generated.UserAssignedIdentitiesProfile{
		ControlPlaneOperators:  map[string]*string{},
		DataPlaneOperators:     map[string]*string{},
		ServiceManagedIdentity: ptr.To(prefix + "service-identity"),
	}
	assigned := map[string]*generated.UserAssignedIdentity{
		prefix + "service-identity": {},
	}
	version := semver.MustParse(openShiftMinorVersion + ".0")
	for name, operator := range config.ControlPlaneOperatorsIdentities {
		if !operator.IsSupportedForOpenshiftVersion(&version) {
			continue
		}
		id := prefix + string(name) + "-identity"
		operators.ControlPlaneOperators[string(name)] = &id
		assigned[id] = &generated.UserAssignedIdentity{}
	}
	for name := range config.AlwaysRequiredDataPlaneOperators(&version) {
		operators.DataPlaneOperators[string(name)] = ptr.To(prefix + string(name) + "-dataplane-identity")
	}
	return &generated.ManagedServiceIdentity{
		Type:                   ptr.To(generated.ManagedServiceIdentityTypeUserAssigned),
		UserAssignedIdentities: assigned,
	}, &generated.OperatorsAuthenticationProfile{UserAssignedIdentities: operators}
}

func nodePoolRequest(version coreapi.Version) (coreapi.VersionedNodePool, error) {
	sample := &v20261001.NodePool{}
	sample.Location = ptr.To("westus3")
	sample.Properties = &generated.NodePoolProperties{
		Version: &generated.NodePoolVersionProfile{ID: ptr.To(openShiftMinorVersion + ".8")},
		Platform: &generated.NodePoolPlatformProfile{
			SubnetID: ptr.To(subnets + "xyz"),
			VMSize:   ptr.To("Standard_D8s_v3"),
			OSDisk: &generated.OsDiskProfile{
				SizeGiB:                ptr.To[int32](64),
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypeStandardSSDLRS),
			},
		},
		Replicas: ptr.To[int32](2),
	}
	v20261001.SetDefaultValuesNodePool(sample)
	nodePool, err := sample.ConvertToInternal(nil)
	if err != nil {
		return nil, err
	}
	request := version.NewNodePool(nodePool)
	request.ClearReadOnlyFields()
	return request, nil
}
