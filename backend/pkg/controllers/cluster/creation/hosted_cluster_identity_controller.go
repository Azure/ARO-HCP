// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package creation

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/util/validation"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const HostedClusterIdentityControllerName = "HostedClusterIdentity"

type hostedClusterIdentitySyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	environmentIdentifier        string
}

// This controller intentionally mirrors Cluster Service's
// ClusterDeploymentNameCalculator. Knowing this identity before creation lets
// backend persist stable management-cluster coordinates and pass the chosen
// name to Cluster Service, instead of learning both values after provisioning.

func NewHostedClusterIdentityController(resourcesDBClient corecosmosstorage.ResourcesDBClient, clustersServiceClient ocm.ClusterServiceClientSpec, informers coreinformers.BackendInformers, environmentIdentifier string) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	return controllerutils.NewClusterWatchingController(HostedClusterIdentityControllerName, resourcesDBClient, informers, nil, time.Minute, &hostedClusterIdentitySyncer{
		resourcesDBClient: resourcesDBClient, clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderClusterLister,
		clustersServiceClient: clustersServiceClient, environmentIdentifier: shortenClusterServiceEnvironment(environmentIdentifier),
	})
}

func (c *hostedClusterIdentitySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.PendingClusterServiceID == nil || cluster.ServiceProviderProperties.ClusterServiceID != nil {
		return nil
	}

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if spc.Status.HostedClusterNamespace != "" && spc.Status.HostedClusterName != "" {
		return nil
	}

	csID := cluster.ServiceProviderProperties.PendingClusterServiceID.ClusterID()
	replacement := spc.DeepCopy()
	if replacement.Status.HostedClusterNamespace == "" {
		replacement.Status.HostedClusterNamespace = controllerutils.HostedClusterNamespace(c.environmentIdentifier, csID)
	}
	if replacement.Status.HostedClusterName == "" {
		name := cluster.CustomerProperties.DNS.BaseDomainPrefix
		if name == "" {
			name, err = c.calculateHostedClusterName(ctx, cluster.Name, csID, key.SubscriptionID, key.ResourceGroupName)
			if err != nil {
				return utils.TrackError(err)
			}
		}
		replacement.Status.HostedClusterName = name
	}
	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *hostedClusterIdentitySyncer) calculateHostedClusterName(ctx context.Context, clusterName, clusterID, subscriptionID, resourceGroupName string) (string, error) {
	candidates := []string{}
	if len(clusterName) <= 15 {
		candidates = append(candidates, normalizeClusterServiceName(clusterName))
	}
	idCandidate := normalizeClusterServiceName(clusterID)
	if len(apierrors.IsDNS1035Label(idCandidate)) == 0 {
		candidates = append(candidates, idCandidate)
	}
	for _, candidate := range candidates {
		unique, err := c.isHostedClusterNameUnique(ctx, candidate, subscriptionID, resourceGroupName)
		if err != nil {
			return "", err
		}
		if unique {
			return candidate, nil
		}
	}
	for attempt := 0; attempt <= 500; attempt++ {
		candidate, err := randomMeaninglessLabel(15)
		if err != nil {
			return "", err
		}
		unique, err := c.isHostedClusterNameUnique(ctx, candidate, subscriptionID, resourceGroupName)
		if err != nil {
			return "", err
		}
		if unique {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to generate a unique HostedCluster name for cluster %s", clusterName)
}

func (c *hostedClusterIdentitySyncer) isHostedClusterNameUnique(ctx context.Context, candidate, subscriptionID, resourceGroupName string) (bool, error) {
	search := fmt.Sprintf("domain_prefix = '%s' and azure.subscription_id = '%s' and azure.resource_group_name = '%s'", candidate, strings.ToLower(subscriptionID), strings.ToLower(resourceGroupName))
	it := c.clustersServiceClient.ListClusters(search)
	for cluster := range it.Items(ctx) {
		azure := cluster.Azure()
		if cluster.DomainPrefix() == candidate && azure != nil && azure.SubscriptionID() == strings.ToLower(subscriptionID) && azure.ResourceGroupName() == strings.ToLower(resourceGroupName) {
			return false, nil
		}
	}
	if err := it.GetError(); err != nil {
		return false, err
	}
	return true, nil
}

func shortenClusterServiceEnvironment(value string) string {
	if value == "integration" {
		return "int"
	}
	return value
}

func normalizeClusterServiceName(value string) string {
	value = strings.ReplaceAll(value, " ", "-")
	if len(value) > 15 {
		value = value[:15]
	}
	return strings.ToLower(strings.Trim(value, "-"))
}

func randomMeaninglessLabel(size int) (string, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	const letters, digits = "abcdefghijklmnopqrstuvwxyz", "0123456789"
	for i := range b {
		if i%2 == 0 {
			b[i] = letters[int(b[i])%len(letters)]
		} else {
			b[i] = digits[int(b[i])%len(digits)]
		}
	}
	return string(b), nil
}
