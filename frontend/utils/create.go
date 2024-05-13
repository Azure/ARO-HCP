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

func CreateJSONFile() error {
	cluster := api.HCPOpenShiftCluster{
		Properties: api.HCPOpenShiftClusterProperties{
			ProvisioningState: arm.ProvisioningState(""),
			Spec: api.ClusterSpec{
				Version: api.VersionProfile{
					ID:           "1.19.0",
					ChannelGroup: "stable",
				},
				DNS: api.DNSProfile{
					BaseDomainPrefix: "xyz",
				},
				Network: api.NetworkProfile{
					NetworkType: api.NetworkType(""),
					PodCIDR:     "10.10.0.0/24",
					ServiceCIDR: "10.10.0.0/24",
					MachineCIDR: "10.10.0.0/24",
					HostPrefix:  0,
				},
				API: api.APIProfile{
					Visibility: api.Visibility("public"),
				},
				FIPS:                          false,
				EtcdEncryption:                false,
				DisableUserWorkloadMonitoring: false,
				Proxy: api.ProxyProfile{
					HTTPProxy:  "",
					HTTPSProxy: "",
					NoProxy:    "",
					TrustedCA:  "",
				},
				Platform: api.PlatformProfile{
					ManagedResourceGroup:   "xyz",
					SubnetID:               "/subscriptions/xyz/resourceGroups/xyz/providers/Microsoft.Network/virtualNetworks/xyz/subnets/xyz",
					OutboundType:           api.OutboundType("loadBalancer"),
					NetworkSecurityGroupID: "/subscriptions/xyz/resourceGroups/xyz/providers/Microsoft.Network/networkSecurityGroups/xyz",
					EtcdEncryptionSetID:    "/subscriptions/xyz/resourceGroups/xyz/providers/Microsoft.Compute/encryptionSets/xyz",
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
