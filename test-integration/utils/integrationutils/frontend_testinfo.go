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

package integrationutils

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/admin/server/server"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type StorageIntegrationTestInfo interface {
	ContentLoader
	DocumentLister

	GetArtifactDir() string
	CosmosClient() database.DBClient

	Cleanup(ctx context.Context)
}

type IntegrationTestInfo struct {
	StorageIntegrationTestInfo
	*ClusterServiceMock

	ArtifactsDir string

	FrontendURL      string
	Frontend         *frontend.Frontend
	AdminURL         string
	AdminAPI         *server.AdminAPI
	adminAPIListener net.Listener
}

func Get20240610ClientFactory(frontendURL string, subscriptionID string) *hcpsdk20240610preview.ClientFactory {
	return api.Must(
		hcpsdk20240610preview.NewClientFactory(subscriptionID, nil,
			&azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					Retry: policy.RetryOptions{
						MaxRetries: -1, // no retries
					},
					Cloud: cloud.Configuration{
						//ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
						Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
							cloud.ResourceManager: {
								Audience: "https://management.core.windows.net/",
								Endpoint: frontendURL,
							},
						},
					},
					InsecureAllowCredentialWithHTTP: true,
					PerCallPolicies: []policy.Policy{
						emptySystemData{},
					},
				},
			},
		),
	)
}

var (
	// clusterCreateOrUpdatePathRegex matches the pattern (case-insensitive):
	// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}
	clusterCreateOrUpdatePathRegex = regexp.MustCompile(
		`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/` + regexp.QuoteMeta(api.ClusterResourceType.String()) + `/[^/]+$`,
	)
)

// emptySystemData provides enough systemdata (normally supplied somewhere in ARM) to enable the server tow ork.
type emptySystemData struct{}

func (d emptySystemData) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(arm.HeaderNameARMResourceSystemData, "{}")
	req.Raw().Header.Set(arm.HeaderNameHomeTenantID, api.TestTenantID)

	// Only set X-Ms-Identity-Url header for cluster create/update requests:
	// In AME environments, ARM sets the header but not for all resource types nor actions.
	// We need to set the headers because in test-integration tests there's no ARM running to set them.
	// We attempt to approximate what ARM does by checking if the request is a cluster create/update request.
	if d.isClusterCreateOrUpdateRequest(req) {
		req.Raw().Header.Set(arm.HeaderNameIdentityURL, api.TestManagedIdentitiesDataPlaneIdentityURL)
	}

	return req.Next()
}

// isClusterCreateOrUpdateRequest checks if the request is cluster create/update request
// where {hcpOpenShiftClusterName} cannot contain slashes.
func (d emptySystemData) isClusterCreateOrUpdateRequest(req *policy.Request) bool {
	path := req.Raw().URL.Path
	method := strings.ToUpper(req.Raw().Method)

	if method != http.MethodPut && method != http.MethodPatch {
		return false
	}

	return clusterCreateOrUpdatePathRegex.MatchString(path)
}

func (s *IntegrationTestInfo) Cleanup(ctx context.Context) {
	s.StorageIntegrationTestInfo.Cleanup(ctx)
	s.ClusterServiceMock.Cleanup(ctx)
}

func resourceIDToDir(resourceID *azcorearm.ResourceID) string {
	if resourceID.Parent == nil {
		return ""
	}
	startingDir := resourceIDToDir(resourceID.Parent)

	switch resourceID.ResourceType.String() {
	case "Microsoft.Resources/tenants":
		return ""
	case "Microsoft.Resources/subscriptions":
		return filepath.Join(
			startingDir,
			"subscriptions",
			resourceID.Name,
		)
	case "Microsoft.Resources/resourceGroups":
		return filepath.Join(
			startingDir,
			"resourceGroups",
			resourceID.Name,
		)

	default:
		if resourceID.Parent.ResourceType.String() == "Microsoft.Resources/resourceGroups" {
			return filepath.Join(
				startingDir,
				resourceID.ResourceType.String(),
				resourceID.Name,
			)
		}

		return filepath.Join(
			startingDir,
			resourceID.ResourceType.Types[len(resourceID.ResourceType.Types)-1],
			resourceID.Name,
		)
	}
}
