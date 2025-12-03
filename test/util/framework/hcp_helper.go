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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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

// UpdateHCPCluster sends a PATCH (BeginUpdate) request for an HCP cluster and waits for completion
// within the provided timeout. It returns the final update response or an error.
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

	poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
	if err != nil {
		return nil, err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse:
		// Verify the operationResult content matches the current cluster model.
		// When an asynchronous operation completes successfully, the RP's result
		// endpoint for the operation is supposed to respond as though the operation
		// were completed synchronously. In production, ARM would call this endpoint
		// automatically. In this context, the poller calls it automatically.
		expect, err := GetHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed getting hcpCluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, err
		}
		err = checkOperationResult(&expect.HcpOpenShiftCluster, &m.HcpOpenShiftCluster)
		if err != nil {
			return nil, err
		}
		return &m.HcpOpenShiftCluster, nil
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
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

// DeleteAllHCPClusters deletes all HCPOpenShiftClusters within a resource group and waits
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteAllHCPClusters for resource group %s", timeout.Minutes(), resourceGroupName))
	defer cancel()

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
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed deleting hcp clusters in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
		}
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
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteNodePool for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := nodePoolsClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
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
			return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
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
			return nil, err
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
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	clusterParams ClusterParams,
	location string,
) (*runtime.Poller[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse], error) {
	cluster := BuildHCPClusterFromParams(clusterParams, location)
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
				Visibility: to.Ptr(hcpsdk20240610preview.Visibility(parameters.APIVisibility)),
			},
			ClusterImageRegistry: &hcpsdk20240610preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20240610preview.ClusterImageRegistryProfileState(parameters.ImageRegistryState)),
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

	return hcpsdk20240610preview.NodePool{
		Location: to.Ptr(location),
		Properties: &hcpsdk20240610preview.NodePoolProperties{
			Version: &hcpsdk20240610preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Replicas: to.Ptr(parameters.Replicas),
			Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20240610preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20240610preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
				},
			},
		},
	}
}
