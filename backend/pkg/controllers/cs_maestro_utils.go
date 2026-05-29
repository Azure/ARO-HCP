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

package controllers

import (
	"context"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
)

// CreateMaestroClientFromCSProvisionShard creates a Maestro client for the given cluster provision shard.
// the client is scoped to the Consumer Name associated to the provision shard, and to
// the source ID associated to the provision shard and the environment specified
// in c.maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func CreateMaestroClientFromCSProvisionShard(
	ctx context.Context, maestroSourceEnvironmentIdentifier string, maestroClientBuilder maestro.MaestroClientBuilder, clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := clusterProvisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := clusterProvisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := clusterProvisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	maestroSourceID := maestro.GenerateMaestroSourceID(maestroSourceEnvironmentIdentifier, clusterProvisionShard.ID())

	maestroClient, err := maestroClientBuilder.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}
