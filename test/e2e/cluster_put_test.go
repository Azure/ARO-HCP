package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

func getIdentityFromFile() arm.ManagedServiceIdentity {
	content, err := os.ReadFile(identityFile)
	if err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	identity := arm.ManagedServiceIdentity{}
	decoder.Decode(&identity)

	return identity
}

func newClusterResource(identity arm.ManagedServiceIdentity) api.HcpOpenShiftCluster {
	apiIdentity := make(map[string]*api.UserAssignedIdentity)
	for key, value := range identity.UserAssignedIdentities {
		apiIdentity[key] = (*api.UserAssignedIdentity)(value)
	}

	newCluster := api.HcpOpenShiftCluster{
		Location: &location,
		Identity: &api.ManagedServiceIdentity{
			Type:                   Ptr(api.ManagedServiceIdentityType(identity.Type)),
			UserAssignedIdentities: apiIdentity,
		},
	}
	return newCluster
}

var _ = Describe("Cluster put operations", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Prepare HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	Context("Negative", func() {
		It("Try to create cluster with managed identities and location", labels.Low, labels.Negative, func(ctx context.Context) {
			genClusterResource := NewDefaultHCPOpenShiftCluster()
			By("Set version ID")
			genClusterResource.Properties.Version.ID = "openshift-v4.18.1"
			By("Set managed resource group")
			genClusterResource.Properties.Platform.ManagedResourceGroup = managedResourceGroup

			identity := getIdentityFromFile()

			clusterResource := newClusterResource(identity)

			log.Logger.Infoln(*clusterResource.Identity.Type)
			for key := range clusterResource.Identity.UserAssignedIdentities {
				log.Logger.Infoln("Gen Identity")
				log.Logger.Infoln(key)
			}

			poller, err := clustersClient.BeginCreateOrUpdate(ctx, customerRGName, clusterName, clusterResource, nil)
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("500 Internal Server Error"))
			Expect(poller).To(BeNil())
		})
	})

})
