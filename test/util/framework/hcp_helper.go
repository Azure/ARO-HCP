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
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/davecgh/go-spew/spew"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func GetAdminRESTConfigForHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) (*rest.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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

	return ret, nil
}

// DeleteHCPCluster deletes an hcp cluster and waits for the operation to complete
func DeleteHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
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

// UpdateHCPCluster sends a PATCH (BeginUpdate) request for an HCP cluster and waits for completion
// within the provided timeout. It returns the final update response or an error.
func UpdateHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20240610preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse:
		return m, nil
	default:
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, fmt.Errorf("unknown type %T", m)
	}
}

// GetHCPCluster fetches an HCP cluster with a timeout
func GetHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
}

// DeleteAllHCPClusters deletes all HCPOpenShiftClusters within a resource group and waits
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hcpClusterNames := []string{}
	hcpClusterPager := hcpClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for hcpClusterPager.More() {
		page, err := hcpClusterPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q: %w", resourceGroupName, err)
		}
		for _, sub := range page.Value {
			hcpClusterNames = append(hcpClusterNames, *sub.Name)
		}
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, hcpClusterName := range hcpClusterNames {
		// https://golang.org/doc/faq#closures_and_goroutines
		hcpClusterName := hcpClusterName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return DeleteHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName, timeout)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		return fmt.Errorf("at least one hcp cluster failed to delete: %w", err)
	}

	return nil
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := nodePoolsClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
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

// GetNodePool fetches a nodepool resource
func GetNodePool(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	timeout time.Duration,
) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
		return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientCreateOrUpdateResponse:
		// TODO someone may want this return value.  We'll have to work it out then.
		//fmt.Printf("#### got back: %v\n", spew.Sdump(m))
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
	timeout time.Duration,
) (hcpsdk20240610preview.ExternalAuthsClientGetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
