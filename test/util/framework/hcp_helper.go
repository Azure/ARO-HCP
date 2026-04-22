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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/onsi/ginkgo/v2"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/ARO-HCP/internal/api"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// checkOperationResult ensures the result model returned by a runtime.Poller
// matches the resource model returned from a GET request.
func checkOperationResult(expectModel, resultModel any) error {
	diff := cmp.Diff(expectModel, resultModel,
		// Add per-model fields that should be ignored in the comparison. For example
		// read-only values that change on their own, or are computed asynchronously
		// and may not be immediately available in the operation result response.
		//
		// Note: I'm anticipating adding "Identity.UserAssignedIdentities" here once
		// the RP takes over fetching client and principal IDs from the Managed Identity
		// service. That would be a concrete example of asynchronously computed fields.
		cmpopts.IgnoreFields(hcpsdk20240610preview.HcpOpenShiftCluster{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20240610preview.NodePool{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20240610preview.ExternalAuth{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20251223preview.NodePool{}, "SystemData"),
	)

	if len(diff) > 0 {
		return fmt.Errorf("operation result model did not match expected model for type %T:\n%s", resultModel, diff)
	}

	return nil
}

func (tc *perItOrDescribeTestContext) GetAdminRESTConfigForHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration, // this is a POST request, so keep the timeout as it's async
) (*rest.Config, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Collect admin credentials for cluster", startTime, finishTime)
	}()

	adminCredentialRequestPoller, err := hcpClient.BeginRequestAdminCredential(
		ctx,
		resourceGroupName,
		hcpClusterName,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start credential request: %w", err)
	}

	operationResult, err := adminCredentialRequestPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish getting creds, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish getting creds: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse:
		return readStaticRESTConfig(m.Kubeconfig)
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func readStaticRESTConfig(kubeconfigContent *string) (*rest.Config, error) {
	ret, err := clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
		if kubeconfigContent == nil {
			return nil, fmt.Errorf("kubeconfig content is nil")
		}
		return clientcmd.Load([]byte(*kubeconfigContent))
	})
	if err != nil {
		return nil, err
	}

	// Skip TLS verification for development environments with self-signed certificates
	if IsDevelopmentEnvironment() {
		ret.Insecure = true
		ret.CAData = nil
		ret.CAFile = ""
	}

	return ret, nil
}

func (tc *perItOrDescribeTestContext) RevokeCredentialsAndWait(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during RevokeCredentialsAndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Collect revoke admin credentials for cluster", startTime, finishTime)
	}()

	poller, err := hcpClient.BeginRevokeCredentials(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to start credential revocation for hcpCluster=%q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish revoking creds, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish revoking creds: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientRevokeCredentialsResponse:
		return nil
	default:
		return fmt.Errorf("unknown type %T", m)
	}
}

// DeleteHCPCluster deletes an hcp cluster and waits for the operation to complete
func DeleteHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
			resp, getErr := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
			if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
				ginkgo.GinkgoLogr.Info("cluster already deleting, waiting for completion",
					"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return waitForHCPClusterDeletion(ctx, hcpClient, resourceGroupName, hcpClusterName)
			}
		}
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// waitForHCPClusterDeletion polls GET on the cluster until it returns 404 (deleted).
func waitForHCPClusterDeletion(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) error {
	for {
		_, err := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				ginkgo.GinkgoLogr.Info("cluster deletion completed",
					"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for already-deleting hcpCluster=%q in resourcegroup=%q to be deleted, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed polling for deletion of hcpCluster=%q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for already-deleting hcpCluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), ctx.Err())
		case <-time.After(StandardPollInterval):
		}
	}
}

// UpdateHCPCluster sends a PATCH (BeginUpdate) request for an HCP cluster and waits for completion
// within the provided timeout. It returns the final update response or an error.
// UpdateHCPCluster updates an HCP cluster using the v20240610preview SDK and waits for the operation to complete.
// Transient 500 and 409 errors are retried automatically with exponential backoff.
func UpdateHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20240610preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20240610preview.HcpOpenShiftCluster
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, stateConflictBackoff, func(ctx context.Context) (bool, error) {
		attempt++
		if attempt > 1 {
			ginkgo.GinkgoLogr.Info("Retrying cluster update",
				"cluster", hcpClusterName,
				"attempt", attempt)
		}

		poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
		if err != nil {
			if isTransientUpdateError(err) {
				return false, nil
			}
			return false, err
		}

		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if isTransientUpdateError(err) {
				return false, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
		}

		switch m := any(operationResult).(type) {
		case hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse:
			expect, err := GetHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return false, fmt.Errorf("failed getting hcpCluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
				}
				return false, err
			}
			if err := checkOperationResult(&expect.HcpOpenShiftCluster, &m.HcpOpenShiftCluster); err != nil {
				return false, err
			}
			hcpOpenShiftCluster = &m.HcpOpenShiftCluster
			return true, nil
		default:
			return false, fmt.Errorf("unknown type %T", m)
		}
	})

	if err != nil && attempt > 1 {
		ginkgo.GinkgoLogr.Info("Cluster update failed after retries",
			"cluster", hcpClusterName,
			"resourceGroup", resourceGroupName,
			"attempts", attempt,
			"error", err.Error())
	}

	return hcpOpenShiftCluster, err
}

// stateConflictBackoff is the retry config for transient state conflicts (ARO-25884).
var stateConflictBackoff = wait.Backoff{
	Steps:    3,               // up to 3 attempts total
	Duration: 1 * time.Minute, // initial wait before first retry
	Factor:   2.0,             // double the wait each retry (1m, 2m)
	Jitter:   0.1,             // ±10% randomization to avoid thundering herd
}

// isTransientUpdateError detects errors worth retrying during cluster updates:
//   - HTTP 500: e.g. Cosmos DB ETag conflict after cluster-service commit
//   - HTTP 409: Conflict
func isTransientUpdateError(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	return responseError.StatusCode == http.StatusInternalServerError ||
		responseError.StatusCode == http.StatusConflict
}

// UpdateHCPCluster20251223 updates an HCP cluster using the v20251223preview SDK and waits for the operation to complete.
// Transient 500 and 409 errors are retried automatically with exponential backoff.
func UpdateHCPCluster20251223(
	ctx context.Context,
	hcpClient *hcpsdk20251223preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20251223preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (*hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster20251223 for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20251223preview.HcpOpenShiftCluster
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, stateConflictBackoff, func(ctx context.Context) (bool, error) {
		attempt++
		if attempt > 1 {
			ginkgo.GinkgoLogr.Info("Retrying cluster update",
				"cluster", hcpClusterName,
				"attempt", attempt)
		}

		poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
		if err != nil {
			if isTransientUpdateError(err) {
				return false, nil
			}
			return false, err
		}

		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if isTransientUpdateError(err) {
				return false, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
		}

		hcpOpenShiftCluster = &operationResult.HcpOpenShiftCluster
		return true, nil
	})

	if err != nil && attempt > 1 {
		ginkgo.GinkgoLogr.Info("Cluster update failed after retries",
			"cluster", hcpClusterName,
			"resourceGroup", resourceGroupName,
			"attempts", attempt,
			"error", err.Error())
	}

	return hcpOpenShiftCluster, err
}

// GetHCPCluster fetches an HCP cluster
func GetHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	return hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
}

// DeleteAllHCPClusters deletes all Clusters within a resource group and waits
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteAllHCPClusters for resource group %s", timeout.Minutes(), resourceGroupName))
	defer cancel()

	var hcpClustersWithoutSizeTag []string
	hcpClusterNames := []string{}
	hcpClusterPager := hcpClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for hcpClusterPager.More() {
		page, err := hcpClusterPager.NextPage(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q: %w", resourceGroupName, err)
		}
		for _, cluster := range page.Value {
			hcpClusterNames = append(hcpClusterNames, *cluster.Name)
			if value, set := cluster.Tags[api.TagClusterSizeOverride]; !set || value == nil || *value != string(api.MinimalControlPlanePodSizing) {
				hcpClustersWithoutSizeTag = append(hcpClustersWithoutSizeTag, *cluster.Name)
			}
		}
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, hcpClusterName := range hcpClusterNames {
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return DeleteHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName, timeout)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed deleting hcp clusters in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
		}
		// remember that Wait only shows the first error, not all the errors.
		return fmt.Errorf("at least one hcp cluster failed to delete: %w", err)
	}

	if len(hcpClustersWithoutSizeTag) > 0 {
		return &NonConformingClustersError{clusters: hcpClustersWithoutSizeTag}
	}

	return nil
}

type NonConformingClustersError struct {
	clusters []string
}

func (e *NonConformingClustersError) Error() string {
	return fmt.Sprintf("the following clusters did not have tags[%s]=%s: %v; we require end-to-end tests to opt into this tag to ensure that the control planes we provision during automated test runs have minimal footprints on our production infrastructure", api.TagClusterSizeOverride, api.MinimalControlPlanePodSizing, e.clusters)
}

// DeleteNodePool deletes a nodepool and waits for the operation to complete
func DeleteNodePool(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteNodePool for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := nodePoolsClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
			resp, getErr := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
			if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
				ginkgo.GinkgoLogr.Info("nodepool already deleting, waiting for completion",
					"nodePool", nodePoolName, "cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return waitForNodePoolDeletion(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed starting nodepool deletion %q for cluster %q in resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish deleting, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish deleting: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// waitForNodePoolDeletion polls GET on the nodepool until it returns 404 (deleted).
func waitForNodePoolDeletion(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) error {
	for {
		_, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				ginkgo.GinkgoLogr.Info("nodepool deletion completed",
					"nodePool", nodePoolName, "cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for already-deleting nodepool=%q in cluster=%q resourcegroup=%q to be deleted, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed polling for deletion of nodepool=%q in cluster=%q resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for already-deleting nodepool=%q in cluster=%q resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), ctx.Err())
		case <-time.After(StandardPollInterval):
		}
	}
}

// GetNodePool fetches a nodepool resource
func GetNodePool(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	return nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
}

// UpdateNodePoolAndWait sends a PATCH (BeginUpdate) request for a nodepool and waits for completion
// within the provided timeout. It returns the final update response or an error.
func UpdateNodePoolAndWait(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	update hcpsdk20240610preview.NodePoolUpdate,
	timeout time.Duration,
) (*hcpsdk20240610preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateNodePoolAndWait for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := nodePoolsClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, nodePoolName, update, nil)
	if err != nil {
		return nil, err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish updating, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish updating: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientUpdateResponse:
		expect, err := GetNodePool(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed getting nodepool=%q in cluster=%q resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, err
		}
		err = checkOperationResult(&expect.NodePool, &m.NodePool)
		if err != nil {
			return nil, err
		}
		return &m.NodePool, nil
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// CreateOrUpdateExternalAuthAndWait creates or updates an external auth on an HCP cluster and waits
func CreateOrUpdateExternalAuthAndWait(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	externalAuth hcpsdk20240610preview.ExternalAuth,
	timeout time.Duration,
) (*hcpsdk20240610preview.ExternalAuth, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateOrUpdateExternalAuthAndWait for external auth %s in cluster %s in resource group %s", timeout.Minutes(), externalAuthName, hcpClusterName, resourceGroupName))
	defer cancel()

	pollerResp, err := externalAuthClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		externalAuth,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}
	operationResult, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish creating or updating, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish creating or updating: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientCreateOrUpdateResponse:
		// Verify the operationResult content matches the current external auth model.
		// When an asynchronous operation completes successfully, the RP's result
		// endpoint for the operation is supposed to respond as though the operation
		// were completed synchronously. In production, ARM would call this endpoint
		// automatically. In this context, the poller calls it automatically.
		expect, err := GetExternalAuth(ctx, externalAuthClient, resourceGroupName, hcpClusterName, externalAuthName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed getting external auth %q in resourcegroup=%q for cluster=%q, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed getting external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
		}
		err = checkOperationResult(&expect.ExternalAuth, &m.ExternalAuth)
		if err != nil {
			return nil, err
		}
		return &m.ExternalAuth, nil
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// CreateExternalAuthAndWait creates a an external auth on an HCP cluster and waits
func GetExternalAuth(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
) (hcpsdk20240610preview.ExternalAuthsClientGetResponse, error) {
	return externalAuthClient.Get(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		&hcpsdk20240610preview.ExternalAuthsClientGetOptions{},
	)
}

// DeleteExternalAuthAndWait deletes a an external auth on an HCP cluster and waits
func DeleteExternalAuthAndWait(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteExternalAuthAndWait for external auth %s in cluster %s in resource group %s", timeout.Minutes(), externalAuthName, hcpClusterName, resourceGroupName))
	defer cancel()

	pollerResp, err := externalAuthClient.BeginDelete(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed deleting external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}
	operationResult, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish deleting, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish deleting: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientDeleteResponse:
		return nil
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}
}

func CreateClusterRoleBinding(ctx context.Context, subject string, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "entra-admins",
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []v1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     subject,
			},
		},
	}, metav1.CreateOptions{})

	if err != nil {
		return fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	return nil
}

// CreateTestDockerConfigSecret creates a Docker config secret for testing pull secret functionality
func CreateTestDockerConfigSecret(host, username, password, email, secretName, namespace string) (*corev1.Secret, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	dockerConfig := DockerConfigJSON{
		Auths: map[string]RegistryAuth{
			host: {
				Email: email,
				Auth:  auth,
			},
		},
	}

	dockerConfigJSON, err := json.Marshal(dockerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker config: %w", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: dockerConfigJSON,
		},
	}, nil
}

func BeginCreateHCPCluster(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	clusterParams ClusterParams,
	location string,
) (*runtime.Poller[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse], error) {
	cluster := BuildHCPClusterFromParams(clusterParams, location)
	logger.Info("Starting HCP cluster creation", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}
	return poller, nil
}

// CreateHCPClusterAndWait Note that the timeout parameter will only take effect if its value is greater than 0. Otherwise,
// the function won't wait for the deployment to be ready.
func CreateHCPClusterAndWait(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20240610preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPClusterAndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName, "version", cluster.Properties.Version.ID, "channelGroup", cluster.Properties.Version.ChannelGroup)
	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	if timeout > 0*time.Second {
		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating: %w", hcpClusterName, resourceGroupName, err)
		}
		switch m := any(operationResult).(type) {
		case hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
			// Verify the operationResult content matches the current cluster model.
			// When an asynchronous operation completes successfully, the RP's result
			// endpoint for the operation is supposed to respond as though the operation
			// were completed synchronously. In production, ARM would call this endpoint
			// automatically. In this context, the poller calls it automatically.
			expect, err := GetHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("failed getting cluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
				}
				return nil, err
			}
			err = checkOperationResult(&expect.HcpOpenShiftCluster, &m.HcpOpenShiftCluster)
			if err != nil {
				return nil, err
			}
			return &m.HcpOpenShiftCluster, nil
		default:
			fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
			return nil, fmt.Errorf("unknown type %T", m)
		}
	} else {
		_, err := poller.Poll(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
		}
		return nil, nil
	}

}

func BuildHCPClusterFromParams(
	parameters ClusterParams,
	location string,
) hcpsdk20240610preview.HcpOpenShiftCluster {

	return hcpsdk20240610preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: parameters.Identity,
		Tags:     parameters.Tags,
		Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20240610preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20240610preview.PlatformProfile{
				ManagedResourceGroup:   to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID: to.Ptr(parameters.NsgResourceID),
				SubnetID:               to.Ptr(parameters.SubnetResourceID),
				OperatorsAuthentication: &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: parameters.UserAssignedIdentitiesProfile,
				}},
			Network: &hcpsdk20240610preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20240610preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20240610preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20240610preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			ClusterImageRegistry: &hcpsdk20240610preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20240610preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			Etcd: &hcpsdk20240610preview.EtcdProfile{
				DataEncryption: &hcpsdk20240610preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20240610preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20240610preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20240610preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20240610preview.KmsEncryptionProfile{
							ActiveKey: &hcpsdk20240610preview.KmsKey{
								VaultName: to.Ptr(parameters.KeyVaultName),
								Name:      to.Ptr(parameters.EtcdEncryptionKeyName),
								Version:   to.Ptr(parameters.EtcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
			Autoscaling: parameters.Autoscaling,
		},
	}
}

func CreateNodePoolAndWait(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20240610preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20240610preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()
	poller, err := nodePoolsClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, nodePoolName, nodePool, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting nodepool creation %q for cluster %q in resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for nodepool=%q for cluster %q in resourcegroup=%q to finish creating: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}
	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse:
		// Verify the operationResult content matches the current node pool model.
		// When an asynchronous operation completes successfully, the RP's result
		// endpoint for the operation is supposed to respond as though the operation
		// were completed synchronously. In production, ARM would call this endpoint
		// automatically. In this context, the poller calls it automatically.
		expect, err := GetNodePool(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed to get nodepool, caused by: %w, error: %w", context.Cause(ctx), err)
			}
			return nil, err
		}
		err = checkOperationResult(&expect.NodePool, &m.NodePool)
		if err != nil {
			return nil, err
		}
		return &m.NodePool, nil
	default:
		fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func BuildNodePoolFromParams(
	parameters NodePoolParams,
	location string,
) hcpsdk20240610preview.NodePool {

	nodePool := hcpsdk20240610preview.NodePool{
		Location: to.Ptr(location),
		Properties: &hcpsdk20240610preview.NodePoolProperties{
			Version: &hcpsdk20240610preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			NodeDrainTimeoutMinutes: parameters.NodeDrainTimeoutMinutes,
			Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20240610preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20240610preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
				},
				AvailabilityZone: to.Ptr(parameters.AvailabilityZone),
			},
		},
	}

	if parameters.AutoScaling != nil {
		nodePool.Properties.AutoScaling = &hcpsdk20240610preview.NodePoolAutoScaling{
			Min: to.Ptr(parameters.AutoScaling.Min),
			Max: to.Ptr(parameters.AutoScaling.Max),
		}
	} else {
		nodePool.Properties.Replicas = to.Ptr(parameters.Replicas)
	}

	return nodePool
}

// Helper to run command on VM
func RunVMCommand(ctx context.Context, tc interface {
	SubscriptionID(ctx context.Context) (string, error)
	AzureCredential() (azcore.TokenCredential, error)
}, resourceGroup, vmName, command string, pollTimeout time.Duration) (string, error) {
	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", err
	}

	azCreds, err := tc.AzureCredential()
	if err != nil {
		return "", err
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, azCreds, nil)
	if err != nil {
		return "", err
	}

	runCommandInput := armcompute.RunCommandInput{
		CommandID: to.Ptr("RunShellScript"),
		Script: []*string{
			to.Ptr(command),
		},
	}

	poller, err := computeClient.BeginRunCommand(ctx, resourceGroup, vmName, runCommandInput, nil)
	if err != nil {
		return "", err
	}

	// Create a timeout context to avoid waiting too long on VM command failures
	// VM commands should complete quickly (within a few minutes at most)
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	result, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		return "", err
	}

	if len(result.Value) > 0 && result.Value[0].Message != nil {
		// Azure Run Command returns output in format:
		// "Enable succeeded: \n[stdout]\n<actual output>\n[stderr]\n<errors>"
		// We need to extract stdout and stderr content
		message := *result.Value[0].Message

		// Find the stdout section
		stdoutStart := strings.Index(message, "[stdout]\n")
		if stdoutStart == -1 {
			// If no stdout marker, return the whole message
			return message, nil
		}

		// Skip past the "[stdout]\n" marker
		stdoutStart += len("[stdout]\n")

		// Find where stderr starts (if present)
		stderrStart := strings.Index(message[stdoutStart:], "\n[stderr]")

		var output string
		if stderrStart == -1 {
			// No stderr marker, take everything after stdout
			output = message[stdoutStart:]
		} else {
			// Take only the stdout section
			output = message[stdoutStart : stdoutStart+stderrStart]

			// Extract and inspect stderr
			stderrAbsoluteStart := stdoutStart + stderrStart + len("\n[stderr]\n")
			if stderrAbsoluteStart < len(message) {
				stderr := strings.TrimSpace(message[stderrAbsoluteStart:])
				if stderr != "" {
					// Return an error if stderr is not empty
					return "", fmt.Errorf("%s", stderr)
				}
			}
		}

		return strings.TrimSpace(output), nil
	}

	return "", nil
}

// Helper to generate SSH key pair
func GenerateSSHKeyPair() (publicKey string, privateKey string, err error) {
	// Generate RSA key pair
	privateKeyData, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	// Encode private key to PEM format
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKeyData),
	}
	privateKeyStr := string(pem.EncodeToMemory(privateKeyPEM))

	// Generate public key in SSH format
	pub, err := ssh.NewPublicKey(&privateKeyData.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicKeyStr := string(ssh.MarshalAuthorizedKey(pub))

	return publicKeyStr, privateKeyStr, nil
}

// Helper to generate kubeconfig
func GenerateKubeconfig(restConfig *rest.Config) (string, error) {
	// Create kubeconfig using proper types
	config := clientcmdapi.NewConfig()

	// Define cluster
	clusterName := "cluster"
	cluster := clientcmdapi.NewCluster()
	cluster.Server = restConfig.Host

	// In development environments, CAData is cleared and Insecure is set to true
	// We need to handle this case by adding insecure-skip-tls-verify
	if len(restConfig.CAData) == 0 || restConfig.Insecure {
		cluster.InsecureSkipTLSVerify = true
	} else {
		cluster.CertificateAuthorityData = restConfig.CAData
	}
	config.Clusters[clusterName] = cluster

	// Define user
	userName := "admin"
	authInfo := clientcmdapi.NewAuthInfo()
	// Support both certificate and token authentication
	if restConfig.BearerToken != "" {
		authInfo.Token = restConfig.BearerToken
	} else {
		authInfo.ClientCertificateData = restConfig.CertData
		authInfo.ClientKeyData = restConfig.KeyData
	}
	config.AuthInfos[userName] = authInfo

	// Define context
	contextName := "admin@cluster"
	context := clientcmdapi.NewContext()
	context.Cluster = clusterName
	context.AuthInfo = userName
	config.Contexts[contextName] = context

	// Set current context
	config.CurrentContext = contextName

	// Marshal to YAML
	kubeconfigBytes, err := clientcmd.Write(*config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kubeconfig: %w", err)
	}

	return string(kubeconfigBytes), nil
}

// convertViaJSON converts between structurally identical types from different SDK versions
// by JSON round-tripping. This is necessary because ClusterParams stores the old SDK types
// but we need to produce new SDK types.
func convertViaJSON[T any](src any) (*T, error) {
	b, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal for type conversion: %w", err)
	}
	var dst T
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&dst); err != nil {
		return nil, fmt.Errorf("failed to unmarshal for type conversion: %w", err)
	}
	return &dst, nil
}

func BuildHCPCluster20251223FromParams(
	parameters ClusterParams,
	location string,
	imageDigestMirrors []*hcpsdk20251223preview.ImageDigestMirror,
) (hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	// Convert identity types from old SDK to new SDK via JSON round-trip
	var identity *hcpsdk20251223preview.ManagedServiceIdentity
	if parameters.Identity != nil {
		var err error
		identity, err = convertViaJSON[hcpsdk20251223preview.ManagedServiceIdentity](parameters.Identity)
		if err != nil {
			return hcpsdk20251223preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert Identity: %w", err)
		}
	}

	var uamis *hcpsdk20251223preview.UserAssignedIdentitiesProfile
	if parameters.UserAssignedIdentitiesProfile != nil {
		var err error
		uamis, err = convertViaJSON[hcpsdk20251223preview.UserAssignedIdentitiesProfile](parameters.UserAssignedIdentitiesProfile)
		if err != nil {
			return hcpsdk20251223preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert UserAssignedIdentitiesProfile: %w", err)
		}
	}

	return hcpsdk20251223preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: identity,
		Tags:     parameters.Tags,
		Properties: &hcpsdk20251223preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20251223preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20251223preview.PlatformProfile{
				ManagedResourceGroup:    to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID:  to.Ptr(parameters.NsgResourceID),
				SubnetID:                to.Ptr(parameters.SubnetResourceID),
				VnetIntegrationSubnetID: to.Ptr(parameters.VnetIntegrationSubnetID),
				OperatorsAuthentication: &hcpsdk20251223preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: uamis,
				},
			},
			Network: &hcpsdk20251223preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20251223preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20251223preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20251223preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			ClusterImageRegistry: &hcpsdk20251223preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20251223preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			Etcd: &hcpsdk20251223preview.EtcdProfile{
				DataEncryption: &hcpsdk20251223preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20251223preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20251223preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20251223preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20251223preview.KmsEncryptionProfile{
							VaultName:  to.Ptr(parameters.KeyVaultName),
							Visibility: to.Ptr(hcpsdk20251223preview.KeyVaultVisibility(parameters.KeyVaultVisibility)),
							ActiveKey: &hcpsdk20251223preview.KmsKey{
								Name:    to.Ptr(parameters.EtcdEncryptionKeyName),
								Version: to.Ptr(parameters.EtcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
			ImageDigestMirrors: imageDigestMirrors,
		},
	}, nil
}

// CreateHCPCluster20251223AndWait creates an HCP cluster using the v20251223preview API and waits for completion.
func CreateHCPCluster20251223AndWait(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20251223preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20251223preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20251223AndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation (v20251223preview)", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	if timeout > 0*time.Second {
		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating: %w", hcpClusterName, resourceGroupName, err)
		}
		switch m := any(operationResult).(type) {
		case hcpsdk20251223preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
			return &m.HcpOpenShiftCluster, nil
		default:
			fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
			return nil, fmt.Errorf("unknown type %T", m)
		}
	} else {
		_, err := poller.Poll(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
		}
		return nil, nil
	}
}

// Verifies that a nodepool created using framework has DiskStorageAccountType set to the framework default "StandardSSD_LRS"
func ValidateNodePoolDiskStorageAccountType(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) error {
	nodePoolResp, err := GetNodePool(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
	if err != nil {
		return fmt.Errorf("failed to get nodepool %s: %w", nodePoolName, err)
	}

	nodePool := nodePoolResp.NodePool

	// Verify the nodepool exists and has the expected structure
	if nodePool.Properties == nil {
		return fmt.Errorf("nodepool %s has no properties", nodePoolName)
	}

	if nodePool.Properties.Platform == nil {
		return fmt.Errorf("nodepool %s has no platform configuration", nodePoolName)
	}

	if nodePool.Properties.Platform.OSDisk == nil {
		return fmt.Errorf("nodepool %s has no OS disk configuration", nodePoolName)
	}

	if nodePool.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		return fmt.Errorf("nodepool %s has no DiskStorageAccountType set", nodePoolName)
	}

	// Verify the framework default (StandardSSD_LRS) overrode the API default (Premium_LRS)
	expectedDiskType := "StandardSSD_LRS"
	actualDiskType := string(*nodePool.Properties.Platform.OSDisk.DiskStorageAccountType)

	if actualDiskType != expectedDiskType {
		return fmt.Errorf("nodepool %s has incorrect DiskStorageAccountType: expected %s (framework default), got %s",
			nodePoolName, expectedDiskType, actualDiskType)
	}

	return nil
}

func HasNodeLabel(nodes []corev1.Node, key, value string, expectedCount ...int) bool {
	count := 0
	for _, node := range nodes {
		if node.Labels[key] == value {
			count++
		}
	}

	if len(expectedCount) == 0 {
		return count > 0
	}

	return count == expectedCount[0]
}

func HasNodeTaint(nodes []corev1.Node, key, value string, effect corev1.TaintEffect, expectedCount ...int) bool {
	count := 0
	for _, node := range nodes {
		for _, taint := range node.Spec.Taints {
			if taint.Key == key && taint.Value == value && taint.Effect == effect {
				count++
				break
			}
		}
	}

	if len(expectedCount) == 0 {
		return count > 0
	}

	return count == expectedCount[0]
}

// CreateNodePoolAndWait20251223 creates a nodepool using the v20251223preview API and waits for completion.
func CreateNodePoolAndWait20251223(
	ctx context.Context,
	nodePoolsClient *hcpsdk20251223preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20251223preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20251223preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait20251223 for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()
	poller, err := nodePoolsClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, nodePoolName, nodePool, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting nodepool creation %q for cluster %q in resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for nodepool=%q for cluster %q in resourcegroup=%q to finish creating: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	// Verify the LRO result body matches a fresh GET, per ARM LRO contract.
	expect, err := GetNodePool20251223(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed to get nodepool, caused by: %w, error: %w", context.Cause(ctx), err)
		}
		return nil, err
	}
	if err := checkOperationResult(expect, &operationResult.NodePool); err != nil {
		return nil, err
	}

	return &operationResult.NodePool, nil
}

// GetNodePool20251223 retrieves a nodepool using the v20251223preview API.
func GetNodePool20251223(
	ctx context.Context,
	nodePoolsClient *hcpsdk20251223preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (*hcpsdk20251223preview.NodePool, error) {
	resp, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.NodePool, nil
}

// GetVirtualMachinesInResourceGroup lists all VMs in the given resource group
// with a polling loop to handle ARM replication delays.
func GetVirtualMachinesInResourceGroup(
	ctx context.Context,
	computeClientFactory *armcompute.ClientFactory,
	resourceGroupName string,
	expectedMinimumCount int,
	timeout time.Duration,
) ([]*armcompute.VirtualMachine, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout,
		fmt.Errorf("timed out waiting for %d VMs in resource group %q", expectedMinimumCount, resourceGroupName))
	defer cancel()

	vmClient := computeClientFactory.NewVirtualMachinesClient()
	const pollInterval = 10 * time.Second

	for {
		var vms []*armcompute.VirtualMachine
		pager := vmClient.NewListPager(resourceGroupName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("caused by: %w, error: %w", context.Cause(ctx), err)
				}
				return nil, fmt.Errorf("failed to list VMs in resource group %q: %w", resourceGroupName, err)
			}
			vms = append(vms, page.Value...)
		}

		if len(vms) >= expectedMinimumCount {
			return vms, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("caused by: %w, error: %w", context.Cause(ctx), ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// BuildHCPClusterFromParamsV20251223 builds a v20251223preview HCP cluster from ClusterParamsV20251223
