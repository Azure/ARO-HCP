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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

const (
	SharedFixtureManifestVersion = 2
	SharedFixtureManifestEnv     = "ARO_HCP_SHARED_FIXTURE_MANIFEST"
	SharedFixtureRunIDEnv        = "ARO_HCP_TEST_RUN_ID"
	SharedFixtureOwnerPIDEnv     = "ARO_HCP_TEST_RUN_OWNER_PID"
	SharedFixtureRunIDTag        = "runID.aro-hcp-ci.redhat.com"
	SharedFixtureLifetime        = 24 * time.Hour
)

type SharedClusterManifest struct {
	Version                 int       `json:"version"`
	RunID                   string    `json:"runID"`
	OwnerPID                int       `json:"ownerPID"`
	ExpiresAt               time.Time `json:"expiresAt"`
	Ready                   bool      `json:"ready"`
	SubscriptionID          string    `json:"subscriptionID"`
	CustomerResourceGroup   string    `json:"customerResourceGroup"`
	ManagedResourceGroup    string    `json:"managedResourceGroup"`
	ClusterName             string    `json:"clusterName"`
	ClusterResourceID       string    `json:"clusterResourceID"`
	NodePoolName            string    `json:"nodePoolName"`
	NodePoolResourceID      string    `json:"nodePoolResourceID"`
	Location                string    `json:"location"`
	Environment             string    `json:"environment"`
	FrontendAddress         string    `json:"frontendAddress"`
	ResourceManagerEndpoint string    `json:"resourceManagerEndpoint"`
}

func WriteSharedClusterManifest(path string, manifest SharedClusterManifest) (retErr error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".shared-cluster-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func ReadSharedClusterManifest() (SharedClusterManifest, error) {
	var manifest SharedClusterManifest
	path := os.Getenv(SharedFixtureManifestEnv)
	if path == "" || !filepath.IsAbs(path) {
		return manifest, fmt.Errorf("%s must identify an absolute manifest path", SharedFixtureManifestEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read shared cluster manifest: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("decode shared cluster manifest: %w", err)
	}
	ownerPID, err := strconv.Atoi(os.Getenv(SharedFixtureOwnerPIDEnv))
	if err != nil || ownerPID <= 0 || manifest.OwnerPID != ownerPID || (ownerPID != os.Getpid() && ownerPID != os.Getppid()) {
		return manifest, fmt.Errorf("shared cluster manifest owner does not match this invocation")
	}
	if manifest.Version != SharedFixtureManifestVersion || manifest.RunID == "" || manifest.RunID != os.Getenv(SharedFixtureRunIDEnv) || !manifest.Ready || !time.Now().Before(manifest.ExpiresAt) {
		return manifest, fmt.Errorf("shared cluster manifest is stale, unready, or belongs to another run")
	}
	if manifest.SubscriptionID == "" || manifest.CustomerResourceGroup == "" || manifest.ManagedResourceGroup == "" || manifest.ClusterName == "" || manifest.Location == "" {
		return manifest, fmt.Errorf("shared cluster manifest is missing resource identity fields")
	}
	expectedID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s", manifest.SubscriptionID, manifest.CustomerResourceGroup, manifest.ClusterName)
	if !strings.EqualFold(expectedID, manifest.ClusterResourceID) {
		return manifest, fmt.Errorf("shared cluster manifest resource ID is inconsistent")
	}
	if manifest.NodePoolName == "" || strings.ContainsAny(manifest.NodePoolName, "/?#") || !strings.EqualFold(expectedID+"/nodePools/"+manifest.NodePoolName, manifest.NodePoolResourceID) {
		return manifest, fmt.Errorf("shared cluster manifest node pool identity is missing or inconsistent")
	}
	if manifest.Environment != os.Getenv("AROHCP_ENV") || manifest.FrontendAddress != os.Getenv("FRONTEND_ADDRESS") || manifest.ResourceManagerEndpoint != os.Getenv("RESOURCE_MANAGER_ENDPOINT") {
		return manifest, fmt.Errorf("shared cluster manifest environment does not match this invocation")
	}
	return manifest, nil
}

type ReadOnlyClusterClient struct {
	manifest  SharedClusterManifest
	client    *hcpsdk20240610preview.HcpOpenShiftClustersClient
	nodePools *hcpsdk20240610preview.NodePoolsClient
}

func SharedReadOnlyCluster(ctx context.Context) (SharedClusterManifest, *ReadOnlyClusterClient, error) {
	manifest, err := ReadSharedClusterManifest()
	if err != nil {
		return manifest, nil, err
	}
	invocation, err := newInvocationContext()
	if err != nil {
		return manifest, nil, err
	}
	clientContext := &perItOrDescribeTestContext{perBinaryInvocationTestContext: invocation}
	subscriptionID, err := clientContext.SubscriptionID(ctx)
	if err != nil {
		return manifest, nil, err
	}
	if subscriptionID != manifest.SubscriptionID || clientContext.Location() != manifest.Location {
		return manifest, nil, fmt.Errorf("shared cluster subscription or location differs from the test environment")
	}
	factory, err := clientContext.Get20240610ClientFactory(ctx)
	if err != nil {
		return manifest, nil, err
	}
	return manifest, &ReadOnlyClusterClient{manifest: manifest, client: factory.NewHcpOpenShiftClustersClient(), nodePools: factory.NewNodePoolsClient()}, nil
}

func (client *ReadOnlyClusterClient) GetNodePool20240610(ctx context.Context) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	manifest, err := ReadSharedClusterManifest()
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientGetResponse{}, err
	}
	if manifest != client.manifest {
		return hcpsdk20240610preview.NodePoolsClientGetResponse{}, fmt.Errorf("shared cluster manifest changed since borrowing")
	}
	return client.nodePools.Get(ctx, manifest.CustomerResourceGroup, manifest.ClusterName, manifest.NodePoolName, nil)
}

func (client *ReadOnlyClusterClient) Get20240610(ctx context.Context, clusterName string) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	if _, err := ReadSharedClusterManifest(); err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse{}, err
	}
	return client.client.Get(ctx, client.manifest.CustomerResourceGroup, clusterName, nil)
}

func (client *ReadOnlyClusterClient) List20240610(ctx context.Context) ([]*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	if _, err := ReadSharedClusterManifest(); err != nil {
		return nil, err
	}
	var clusters []*hcpsdk20240610preview.HcpOpenShiftCluster
	pager := client.client.NewListByResourceGroupPager(client.manifest.CustomerResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, page.Value...)
	}
	return clusters, nil
}
