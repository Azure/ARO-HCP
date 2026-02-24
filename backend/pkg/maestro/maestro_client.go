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

// Client is an interface to interact with the Maestro API using Maestro Bundles.
// Although the Maestro Go library provides a client that returns a workv1client.ManifestWorkInterface, we define our
// own interface because workv1client.ManifestWorkInterface is an interface to interact with Open Cluster Management ManifestWorks,
// it just happens to be that the Maestro Go library provides a client that allows to interact with Maestro Bundles abstracted
// as workv1.ManifestWork resources, which is how the Maestro Go Client abstracts them. However, Open Cluster Management ManifestWorks
// are not necessarily Maestro bundles, so we define this type to make more explicit about the fact that it is a Maestro Client
// based on the ManifestWorks abstraction, at both the type level and in its method signatures.
type Client interface {
	CreateMaestroBundle(ctx context.Context, maestroBundle *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error)
	GetMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.GetOptions) (*workv1.ManifestWork, error)
	DeleteMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.DeleteOptions) error
	PatchMaestroBundle(ctx context.Context, bundleMetadataName string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *workv1.ManifestWork, err error)
	ListMaestroBundles(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error)
}

// client is a client that wraps the workv1client.MaestroManifestWorksInterface interface
// and implements the Client interface, delegating the actual work to the workv1client.MaestroManifestWorksInterface interface.
type client struct {
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
	// events. See https://github.com/open-cluster-management-io/enhancements/tree/main/enhancements/sig-architecture/224-event-based-manifestwork#terminology
	// for more details.
	maestroSourceID string
	// maestroConsumerName is the name of the Maestro Consumer. A Maestro Consumer
	// represents a target for resource delivery. In ARO-HCP this is a
	// Management Cluster, where a Maestro Agent is deployed. The Maestro Agent
	// is then configured with a Consumer Name.
	maestroConsumerName string
}

var _ Client = &client{}

func (c *client) CreateMaestroBundle(ctx context.Context, bundle *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error) {
	return c.maestroManifestWorksInterface.Create(ctx, bundle, opts)
}

func (c *client) GetMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.GetOptions) (*workv1.ManifestWork, error) {
	return c.maestroManifestWorksInterface.Get(ctx, bundleMetadataName, opts)
}

func (c *client) DeleteMaestroBundle(ctx context.Context, bundleMetadataName string, opts metav1.DeleteOptions) error {
	return c.maestroManifestWorksInterface.Delete(ctx, bundleMetadataName, opts)
}

func (c *client) PatchMaestroBundle(ctx context.Context, bundleMetadataName string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *workv1.ManifestWork, err error) {
	return c.maestroManifestWorksInterface.Patch(ctx, bundleMetadataName, pt, data, opts, subresources...)
}

func (c *client) ListMaestroBundles(ctx context.Context, opts metav1.ListOptions) (*workv1.ManifestWorkList, error) {
	return c.maestroManifestWorksInterface.List(ctx, opts)
}

// NewClient creates a new Maestro Client that allows you to interact with Maestro using the Open Cluster Management ManifestWorks abstraction
// It uses the provided Maestro REST and GRPC API endpoints to interact with it. Both endpoints are assumed to be pointing to the same Maestro server.
// The Maestro Client is scoped to a specific Maestro Source ID and a specific Maestro Consumer Name. See the `client` type for more details.
func NewClient(
	ctx context.Context,
	maestroRESTAPIEndpoint string, maestroGRPCAPIEndpoint string, maestroConsumerName string, maestroSourceID string,
) (Client, error) {
	restClient := newRESTClient(maestroRESTAPIEndpoint)
	grpcClient, err := newGRPCSourceWorkClient(ctx, maestroGRPCAPIEndpoint, restClient, maestroSourceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create maestro grpc source work client: %w", err))
	}

	maestroManifestWorksInterface := newMaestroManifestWorksClient(grpcClient, maestroConsumerName)
	return &client{
		maestroManifestWorksInterface: maestroManifestWorksInterface,
		maestroSourceID:               maestroSourceID,
		maestroConsumerName:           maestroConsumerName,
	}, nil
}

// GenerateMaestroSourceID generates a Maestro Source ID of the form "<envName>-<provisionShardID>".
// The Maestro Source ID is used to identify resources created by a given maestro source. See the `client` type for more details about it.
// This method can be used to calculate Maestro Source IDs in the same way that Clusters Service does.
// The <envName> and <provisionShardID> concepts and terminology are inherited from Clusters Service.
// The reason in CS we generate the Maestro Source ID in that way is because we wanted to have a different Maestro Source ID per Provision Shard (management cluster),
// because a Maestro server can manage N management clusters and we want to have a different Maestro Source ID per management cluster. We also have
// the concept of envName, which allowed us to have a different Maestro Source IDs between different ARO-HCP environments, as well as cover the case
// of the CS CI where multiple independent CS deployments use the same Maestro server configured with a single Management Cluster.
// The concept of envName is inherited from CS. It allows to have a different Maestro Source ID between different ARO-HCP environments as well as
// multiple independent deployments within the same ARO-HCP environment that use a single Maestro server and provision shard (management cluster).
// It is important not to change this logic without a proper migration plan of previously created Maestro bundles associated to a given Provision Shard.
func GenerateMaestroSourceID(envName string, provisionShardID string) string {
	return fmt.Sprintf("%s-%s", envName, provisionShardID)
}

// newRESTClient creates a REST client for the Maestro API. The Maestro REST client
// allows to perform a subset (but not all) of actions against the Maestro API.
func newRESTClient(endpoint string) *maestroopenapi.APIClient {
	maestroRESTClientConfig := &maestroopenapi.Configuration{
		DefaultHeader: map[string]string{},
		UserAgent:     "ARO-HCP-Backend",
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

// newGRPCSourceWorkClient creates a new GRPC Source Work client for the Maestro API.
// The Maestro GRPC SourcE Work client allows to interact with Maestro Bundles using the GRPC protocol and
// it allows to perform a subset (but not all) of actions against the Maestro API.
func newGRPCSourceWorkClient(ctx context.Context, endpoint string, maestroRESTClient *maestroopenapi.APIClient, sourceID string) (workv1client.WorkV1Interface, error) {
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

// newMaestroManifestWorksClient creates a new Maestro Manifest Works client for the Maestro API. It returns
// a workv1client.ManifestWorkInterface that allows to interact with Maestro Bundles using the Open Cluster Management ManifestWorks abstraction.
func newMaestroManifestWorksClient(maestroGRPCSourceWorkClient workv1client.WorkV1Interface, maestroConsumerName string) workv1client.ManifestWorkInterface {
	manifestWorksClient := maestroGRPCSourceWorkClient.ManifestWorks(maestroConsumerName)
	return manifestWorksClient
}

// getDefaultServerHealthinessTimeout returns the default server healthiness timeout for the maestro
// grpc client configuration. This checks that we receive a heartbeat at least every 20 seconds. If no
// heartbeat is received, it will reconnect.
//
// It's important that our value is higher than what's configured for the server (currently 10 seconds)
// See https://github.com/openshift-online/maestro/blob/ff77e644/pkg/config/grpc_server.go#L66
// for reference.
//
// If at some point the default changes to a higher value than the one established here, or
// the maestro server configuration changes to a higher default than what's here, this will need to be revisited.
func getDefaultServerHealthinessTimeout() *time.Duration {
	defaultServerHealthinessTimeout := 20 * time.Second
	return &defaultServerHealthinessTimeout
}
