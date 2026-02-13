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

package maestro

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	workv1client "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	"open-cluster-management.io/sdk-go/pkg/cloudevents/generic/options/grpc"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"
	maestrogrpcsource "github.com/openshift-online/maestro/pkg/client/cloudevents/grpcsource"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// SimpleMaestroClient is a simple client that wraps the MaestroManifestWorksInterface
// and provides a simple interface for creating, getting, deleting, updating and listing Maestro bundles.
// Although we already have the workv1client.ManifestWorkInterface, because that interface is purely for ManifestWorks
// which are not necessarily Maestro bundles we add this type to make more explicit that this is a Maestro client, at
// the type level as well as with different method signatures.
type SimpleMaestroClient interface {
	CreateMaestroBundle(ctx context.Context, maestroBundle *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error)
	GetMaestroBundle(ctx context.Context, bundleName string, opts metav1.GetOptions) (*workv1.ManifestWork, error)
	DeleteMaestroBundle(ctx context.Context, bundleName string, opts metav1.DeleteOptions) error
	PatchMaestroBundle(ctx context.Context, bundleName string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *workv1.ManifestWork, err error)
	ListMaestroBundles(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error)
}

type simpleMaestroClient struct {
	// maestroManifestWorksInterface is the interface that allows to interact with
	// the Maestro API using Maestro Bundles. The interface targets a specific
	// Maestro Consumer and it is configured with a specific Maestro Source ID.
	maestroManifestWorksInterface workv1client.ManifestWorkInterface

	// maestroSourceID is the source ID of the Maestro client. A Maestro Source ID
	// represents the owner/producer of the Maestro Bundles.
	// maestroManifestWorksInterface is scoped to this source ID. This means that
	// the visibility of the Maestro bundles is limited to the Maestro bundles owned
	// by the same source ID Multiple applications or multiple instances of the
	// same application can use the same source ID and they will receive the same
	// events.
	// Not used as of now but it might be useful to have it here.
	maestroSourceID string
	// maestroConsumerName is the name of the Maestro Consumer. A Maestro Consumer
	// represents a target for resource delivery. In ARO-HCP this is a
	// Management Cluster, where a Maestro Agent is deployed. The Maestro Agent
	// is then configured with a Consumer Name.
	maestroConsumerName string
}

func (c *simpleMaestroClient) CreateMaestroBundle(ctx context.Context, bundle *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error) {
	return c.maestroManifestWorksInterface.Create(ctx, bundle, opts)
}

func (c *simpleMaestroClient) GetMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.GetOptions) (*workv1.ManifestWork, error) {
	return c.maestroManifestWorksInterface.Get(ctx, bundleMetadataName, opts)
}

func (c *simpleMaestroClient) DeleteMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.DeleteOptions) error {
	return c.maestroManifestWorksInterface.Delete(ctx, bundleMetadataName, opts)
}

func (c *simpleMaestroClient) PatchMaestroBundle(ctx context.Context, bundleMetadataName string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *workv1.ManifestWork, err error) {
	return c.maestroManifestWorksInterface.Patch(ctx, bundleMetadataName, pt, data, opts, subresources...)
}

func (c *simpleMaestroClient) ListMaestroBundles(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error) {
	return c.maestroManifestWorksInterface.List(ctx, opts)
}

func newMaestroRESTClient(endpoint string) *maestroopenapi.APIClient {
	maestroRESTClientConfig := &maestroopenapi.Configuration{
		DefaultHeader: map[string]string{},
		UserAgent:     "ARO-HCP-Backend/TODOversion (Go ; pod/TODOidentity)",
		Debug:         false,
		Servers: maestroopenapi.ServerConfigurations{{
			URL: endpoint,
		}},
		OperationServers: map[string]maestroopenapi.ServerConfigurations{},
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					//nolint:gosec
					InsecureSkipVerify: true, // TODO pass TLS certs from config
				}},
			Timeout: 30 * time.Second,
		},
	}

	restClient := maestroopenapi.NewAPIClient(maestroRESTClientConfig)
	return restClient
}

func newMaestroGRPCSourceWorkClient(ctx context.Context, endpoint string, maestroRESTClient *maestroopenapi.APIClient, sourceID string) (workv1client.WorkV1Interface, error) {
	logger := utils.LoggerFromContext(ctx)
	ocmLogger := NewLogrToOCMLoggerAdapter(logger)
	grpcOptions := &grpc.GRPCOptions{
		Dialer: &grpc.GRPCDialer{
			URL: endpoint,
		},
		ServerHealthinessTimeout: getDefaultServerHealthinessTimeout(),
	}
	return maestrogrpcsource.NewMaestroGRPCSourceWorkClient(ctx, ocmLogger, maestroRESTClient, grpcOptions, sourceID)
}

func newMaestroManifestWorksClient(maestroGRPCSourceWorkClient workv1client.WorkV1Interface, maestroConsumerName string) workv1client.ManifestWorkInterface {
	manifestWorksClient := maestroGRPCSourceWorkClient.ManifestWorks(maestroConsumerName)
	return manifestWorksClient
}

func NewSimpleMaestroClient(
	ctx context.Context,
	maestroRESTAPIEndpoint string, maestroGRPCAPIEndpoint string, maestroConsumerName string, maestroSourceID string,
) (SimpleMaestroClient, error) {
	restClient := newMaestroRESTClient(maestroRESTAPIEndpoint)
	grpcClient, err := newMaestroGRPCSourceWorkClient(ctx, maestroGRPCAPIEndpoint, restClient, maestroSourceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create maestro grpc source work client: %w", err))
	}

	maestroManifestWorksInterface := newMaestroManifestWorksClient(grpcClient, maestroConsumerName)
	return &simpleMaestroClient{
		maestroManifestWorksInterface: maestroManifestWorksInterface,
		maestroSourceID:               maestroSourceID,
		maestroConsumerName:           maestroConsumerName,
	}, nil
}

// getDefaultServerHealthinessTimeout returns the default server healthiness timeout for the maestro
// grpc client configuration. This checks that we receive a heartbeat at least every 20 seconds. If no
// heartbeat is received, it will reconnect.
//
// It's important that our value is higher than what's configured for the server (currently 10 seconds)
// See https://github.com/openshift-online/maestro/blob/ff77e644/pkg/config/grpc_server.go#L66
// for reference.
//
// If at some point the default changes to a higher value than the cs provided value, or
// the maestro server configuration changes to a higher default than what's here, this will need to be revisited.
func getDefaultServerHealthinessTimeout() *time.Duration {
	defaultServerHealthinessTimeout := 20 * time.Second
	return &defaultServerHealthinessTimeout
}

// GenerateMaestroSourceId concatenates the envName and shardId to form a
// maestro's source id. The envName and shardId are separate by "-" resulting in a sourceId
// of the form "<envName>-<shardId>".
// NOTE: The sourceId is used to identify resources created by a given maestro source.
// The reason in CS we generate them in that way is because we wanted to have a different source per provision shard (management cluster) as a
// Maestro server can manage N management clusters and we want to have a different source per management cluster. We also have the concept of envName
// which allowed us to have a different source per aro-hcp environment. This also covered the case of the CS CI where multiple independent CS deployments
// use the same Maestro server configured with a single Management Cluster.
// The Maestro Source ID represents the owner/producer of the Maestro Bundles.
// maestroManifestWorksInterface is scoped to this source ID. This means that
// the visibility of the Maestro bundles is limited to the Maestro bundles owned
// by the same source ID Multiple applications or multiple instances of the
// same application can use the same source ID and they will receive the same
// events.
// See https://github.com/open-cluster-management-io/enhancements/tree/main/enhancements/sig-architecture/224-event-based-manifestwork#terminology
// for more details. It is then important not to change this logic without a proper migration plan of previously created
// maestro's bundles for a given shard.
// envName is inherited from CS where it is passed during at deployment time as a configuration parameter and it is
// formed with the following format: arohcp{{ .ctx.environment }}" where ctx.environment is the key of the environment in the config.yaml file.
// For example, a value in production where the environment key in config.yaml is `prod` would translate as the envName configuration
// parameter being `arohcpprod`
// The allowed value of the envName configuration parameter is restricted to 10 characters and validated during the component startup. This
// restriction is very important to maintain because it is used as part of K8s Names in the K8s resources created by CS on the ManagementCluster
// side and when increased to more length than that K8s name/namespace length limits were being reached.
func GenerateMaestroSourceID(envName string, provisionShardID string) string {
	return fmt.Sprintf("%s-%s", envName, provisionShardID)
}
