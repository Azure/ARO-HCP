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

package datadumpcontrollers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type csStateDump struct {
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient
	csClient        ocm.ClusterServiceClientSpec

	// nextDumpChecker ensures we don't hotloop from any source.
	nextDumpChecker controllerutils.CooldownChecker
}

// NewCSStateDumpController periodically fetches cluster-service state for each cluster and dumps it to logs.
func NewCSStateDumpController(
	cosmosClient database.DBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	csClient ocm.ClusterServiceClientSpec,
) controllerutils.Controller {
	syncer := &csStateDump{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:    cosmosClient,
		csClient:        csClient,
		nextDumpChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
	}

	return controllerutils.NewClusterWatchingController(
		"CSStateDump",
		cosmosClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *csStateDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.nextDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	// Get the cluster from cosmos to retrieve the ClusterServiceID
	cluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		// Cluster doesn't exist in cosmos, nothing to dump
		return nil
	}
	if err != nil {
		logger.Error(err, "failed to get cluster from cosmos for CS state dump")
		return nil // best effort, don't fail
	}

	csID := cluster.ServiceProviderProperties.ClusterServiceID
	if len(csID.String()) == 0 {
		// No ClusterServiceID yet, cluster hasn't been registered with CS
		return nil
	}

	// Fetch cluster state from cluster-service
	csCluster, err := c.csClient.GetCluster(ctx, csID)
	if err != nil {
		logger.Error(err, "failed to get cluster from cluster-service for CS state dump")
		// Continue with what we have
	}
	// Convert to structured JSON for logging
	var clusterData map[string]any
	if csCluster != nil {
		clusterData, err = csObjectToMap(csCluster)
		if err != nil {
			logger.Error(err, "failed to serialize cluster-service cluster to JSON")
			// Continue with what we have
		}
	}

	logger.Info("cluster-service state dump",
		"clusterServiceID", csID.String(),
		"csCluster", clusterData,
	)

	// Fetch and dump node pools
	allNodePools, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list node pools from cosmos for CS state dump")
		// best effort, don't fail
		return nil
	}

	for _, nodePool := range allNodePools.Items(ctx) {
		npCSID := nodePool.ServiceProviderProperties.ClusterServiceID
		if len(npCSID.String()) == 0 {
			// No ClusterServiceID yet, node pool hasn't been registered with CS
			continue
		}

		csNodePool, err := c.csClient.GetNodePool(ctx, npCSID)
		if err != nil {
			logger.Error(err, "failed to get node pool from cluster-service for CS state dump",
				"nodePoolClusterServiceID", npCSID.String(),
			)
			continue
		}

		var nodePoolData map[string]any
		if csNodePool != nil {
			nodePoolData, err = csObjectToMap(csNodePool)
			if err != nil {
				logger.Error(err, "failed to serialize cluster-service node pool to JSON",
					"nodePoolClusterServiceID", npCSID.String(),
				)
				continue
			}
		}

		logger.Info("cluster-service node pool state dump",
			"clusterServiceID", csID.String(),
			"nodePoolClusterServiceID", npCSID.String(),
			"csNodePool", nodePoolData,
		)
	}
	if err := allNodePools.GetError(); err != nil {
		logger.Error(err, "failed to iterate node pools from cosmos for CS state dump")
	}

	return nil
}

func (c *csStateDump) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// csObjectToMap serializes a cluster-service object to JSON and then decodes it into a map[string]any
// for structured logging. This approach handles the ocm-sdk types which use custom JSON marshaling.
func csObjectToMap(obj any) (map[string]any, error) {
	if obj == nil {
		return nil, nil
	}

	// Marshal to JSON
	jsonBytes, err := MarshalClusterServiceAny(obj)
	if err != nil {
		return nil, err
	}

	// Unmarshal into map[string]any
	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// cluster service types fight the standard golang stack and don't conform to standard json interfaces.
func MarshalClusterServiceAny(clusterServiceData any) ([]byte, error) {
	switch castObj := clusterServiceData.(type) {
	case *arohcpv1alpha1.Cluster:
		buf := &bytes.Buffer{}
		if err := arohcpv1alpha1.MarshalCluster(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *arohcpv1alpha1.ClusterAutoscaler:
		buf := &bytes.Buffer{}
		if err := arohcpv1alpha1.MarshalClusterAutoscaler(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *arohcpv1alpha1.ExternalAuth:
		buf := &bytes.Buffer{}
		if err := arohcpv1alpha1.MarshalExternalAuth(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *arohcpv1alpha1.NodePool:
		buf := &bytes.Buffer{}
		if err := arohcpv1alpha1.MarshalNodePool(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *arohcpv1alpha1.ProvisionShard:
		buf := &bytes.Buffer{}
		if err := arohcpv1alpha1.MarshalProvisionShard(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *cmv1.HypershiftConfig:
		buf := &bytes.Buffer{}
		if err := cmv1.MarshalHypershiftConfig(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown type: %T", castObj)
	}
}
