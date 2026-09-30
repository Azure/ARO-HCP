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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	armhcp "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func NewRPClientFactory(ctx context.Context) (*armhcp.ClientFactory, error) {
	inv := invocationContext()
	creds, err := inv.getAzureCredentials()
	if err != nil {
		return nil, fmt.Errorf("get RP collection credentials: %w", err)
	}
	subscriptions, err := armsubscriptions.NewClientFactory(creds, inv.getClientFactoryOptions())
	if err != nil {
		return nil, fmt.Errorf("create subscription client: %w", err)
	}
	subscriptionID, err := inv.getSubscriptionID(ctx, subscriptions.NewClient())
	if err != nil {
		return nil, fmt.Errorf("resolve RP collection subscription: %w", err)
	}
	return armhcp.NewClientFactory(subscriptionID, creds, inv.getHCPClientFactoryOptions())
}

func CollectRPResourcesAtEndOfRun(ctx context.Context) error {
	outputDir := artifactDir()
	if outputDir == "" {
		fmt.Fprintln(os.Stderr, "WARNING: ARTIFACT_DIR is not set, skipping RP resource collection")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resourceGroups, err := trackedRPResourceGroups(SharedDir())
	if err != nil {
		return err
	}
	if len(resourceGroups) == 0 {
		fmt.Fprintln(os.Stderr, "WARNING: no tracked resource groups in SHARED_DIR, skipping RP resource collection")
		return nil
	}
	clientFactory, err := NewRPClientFactory(ctx)
	if err != nil {
		return err
	}
	return collectRPResources(ctx, clientFactory, resourceGroups, filepath.Join(outputDir, "rp-resources"))
}

func trackedRPResourceGroups(sharedDir string) ([]string, error) {
	if sharedDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		return nil, fmt.Errorf("read tracked resource groups in %q: %w", sharedDir, err)
	}
	const prefix = "tracked-resource-group_"
	var resourceGroups []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if name := strings.TrimPrefix(entry.Name(), prefix); name != "" {
			resourceGroups = append(resourceGroups, name)
		}
	}
	return resourceGroups, nil
}

type collectedNodePool struct {
	ResourceGroup string           `json:"resourceGroup"`
	Cluster       string           `json:"cluster"`
	NodePool      *armhcp.NodePool `json:"nodePool"`
}

type collectedExternalAuth struct {
	ResourceGroup string               `json:"resourceGroup"`
	Cluster       string               `json:"cluster"`
	ExternalAuth  *armhcp.ExternalAuth `json:"externalAuth"`
}

func collectRPResources(ctx context.Context, clientFactory *armhcp.ClientFactory, resourceGroups []string, outputDir string) error {
	clusters := []*armhcp.HcpOpenShiftCluster{}
	nodePools := []collectedNodePool{}
	externalAuths := []collectedExternalAuth{}
	var collectionErrors []error
	recordError := func(err error) {
		fmt.Fprintf(os.Stderr, "WARNING: RP resource collection: %v\n", err)
		collectionErrors = append(collectionErrors, err)
	}

	collectCluster := func(cluster *armhcp.HcpOpenShiftCluster, resourceGroup string) {
		if cluster == nil {
			return
		}
		clusters = append(clusters, cluster)
		clusterName := ptr.Deref(cluster.Name, "")
		if resourceGroup == "" || clusterName == "" {
			recordError(fmt.Errorf("cluster %q has no resource group or name", ptr.Deref(cluster.ID, "")))
			return
		}
		nodePager := clientFactory.NewNodePoolsClient().NewListByParentPager(resourceGroup, clusterName, nil)
		for nodePager.More() {
			page, err := nodePager.NextPage(ctx)
			if err != nil {
				if !isRPResourceNotFound(err) {
					recordError(fmt.Errorf("list node pools for %s/%s: %w", resourceGroup, clusterName, err))
				}
				break
			}
			for _, nodePool := range page.Value {
				nodePools = append(nodePools, collectedNodePool{ResourceGroup: resourceGroup, Cluster: clusterName, NodePool: nodePool})
			}
		}
		authPager := clientFactory.NewExternalAuthsClient().NewListByParentPager(resourceGroup, clusterName, nil)
		for authPager.More() {
			page, err := authPager.NextPage(ctx)
			if err != nil {
				if !isRPResourceNotFound(err) {
					recordError(fmt.Errorf("list external auths for %s/%s: %w", resourceGroup, clusterName, err))
				}
				break
			}
			for _, externalAuth := range page.Value {
				externalAuths = append(externalAuths, collectedExternalAuth{ResourceGroup: resourceGroup, Cluster: clusterName, ExternalAuth: externalAuth})
			}
		}
	}

	clusterClient := clientFactory.NewHcpOpenShiftClustersClient()
	for _, resourceGroup := range resourceGroups {
		pager := clusterClient.NewListByResourceGroupPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if !isRPResourceNotFound(err) {
					recordError(fmt.Errorf("list clusters in %s: %w", resourceGroup, err))
				}
				break
			}
			for _, cluster := range page.Value {
				collectCluster(cluster, resourceGroup)
			}
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		recordError(fmt.Errorf("create RP resource output directory: %w", err))
		return errors.Join(collectionErrors...)
	}
	for _, artifact := range []struct {
		name string
		data any
	}{
		{name: "clusters.json", data: clusters},
		{name: "nodepools.json", data: nodePools},
		{name: "externalauths.json", data: externalAuths},
	} {
		data, err := json.MarshalIndent(artifact.data, "", "  ")
		if err != nil {
			recordError(fmt.Errorf("marshal %s: %w", artifact.name, err))
			continue
		}
		if err := os.WriteFile(filepath.Join(outputDir, artifact.name), data, 0644); err != nil {
			recordError(fmt.Errorf("write %s: %w", artifact.name, err))
		}
	}
	return errors.Join(collectionErrors...)
}

func isRPResourceNotFound(err error) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}
