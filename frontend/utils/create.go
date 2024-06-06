package main

import (
	"encoding/json"
	"os"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func main() {
	err := CreateJSONFile()
	if err != nil {
		panic(err)
	}
}

// CreateJSONFile creates a base cluster JSON file for use with testing frontend to create clusters
func CreateJSONFile() error {
	cluster := api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				Type: "osd-4",
			},
		},
		Properties: api.HCPOpenShiftClusterProperties{
			ProvisioningState: arm.ProvisioningState(""),
			Spec: api.ClusterSpec{
				Version: api.VersionProfile{
					ID:           "openshift-v4.15.11",
					ChannelGroup: "stable",
				},
				DNS: api.DNSProfile{
					BaseDomainPrefix: "xyz",
				},
				Network: api.NetworkProfile{
					NetworkType: api.NetworkType(""),
					PodCIDR:     "10.128.0.0/14",
					ServiceCIDR: "172.30.0.0/16",
					MachineCIDR: "10.0.0.0/16",
					HostPrefix:  0,
				},
				API: api.APIProfile{
					Visibility: api.Visibility("public"),
				},
				FIPS:                          false,
				EtcdEncryption:                false,
				DisableUserWorkloadMonitoring: false,
				Platform: api.PlatformProfile{
					ManagedResourceGroup:   "dev-test-mrg",
					SubnetID:               "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.Network/virtualNetworks/xyz/subnets/xyz",
					OutboundType:           api.OutboundType("loadBalancer"),
					NetworkSecurityGroupID: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.Network/networkSecurityGroups/xyz",
					EtcdEncryptionSetID:    "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.Compute/encryptionSets/xyz",
				},
				IssuerURL:    "",
				ExternalAuth: api.ExternalAuthConfigProfile{},
				Ingress:      []*api.IngressProfile{},
			},
		},
	}

	data, err := json.MarshalIndent(cluster, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile("cluster.json", data, 0643)
	if err != nil {
		return err
	}

	return nil
}
