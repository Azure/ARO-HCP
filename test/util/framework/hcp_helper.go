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
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/davecgh/go-spew/spew"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HostedClusterVerifier interface {
	Name() string
	Verify(ctx context.Context, restConfig *rest.Config) error
}

type verifyImageRegistryDisabled struct{}

func (v verifyImageRegistryDisabled) Name() string {
	return "VerifyImageRegistryDisabled"
}

func (v verifyImageRegistryDisabled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.CoreV1().Services("openshift-image-registry").Get(ctx, "image-registry", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("image-registry service should not exist, but it does")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("wrong type of error: %T, %v", err, err)
	}

	_, err = kubeClient.AppsV1().Deployments("openshift-image-registry").Get(ctx, "image-registry", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("image-registry deployment should not exist, but it does")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("wrong type of error: %T, %v", err, err)
	}

	return nil
}

func VerifyImageRegistryDisabled() HostedClusterVerifier {
	return verifyImageRegistryDisabled{}
}

type verifyBasicAccessImpl struct{}

func (v verifyBasicAccessImpl) Name() string {
	return "VerifyBasicAccess"
}

func (v verifyBasicAccessImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.CoreV1().Services("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	return nil
}

func verifyBasicAccess() HostedClusterVerifier {
	return verifyBasicAccessImpl{}
}

var standardVerifiers = []HostedClusterVerifier{
	verifyBasicAccess(),
}

func VerifyHCPCluster(ctx context.Context, adminRESTConfig *rest.Config, additionalVerifiers ...HostedClusterVerifier) error {
	allVerifiers := append(standardVerifiers, additionalVerifiers...)

	// if these start taking a long time, run in parallel
	errs := []error{}
	for _, verifier := range allVerifiers {
		err := verifier.Verify(ctx, adminRESTConfig)
		if err != nil {
			errs = append(errs, fmt.Errorf("%v failed: %w", verifier.Name(), err))
		}
	}

	return errors.Join(errs...)
}

func GetAdminRESTConfigForHCPCluster(
	ctx context.Context,
	hcpClient *hcpapi20240610.HcpOpenShiftClustersClient,
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
	case hcpapi20240610.HcpOpenShiftClustersClientRequestAdminCredentialResponse:
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

	// we are doing this because there's a serious bug.  I haven't got an ETA on a fix, but if we fail to correct it, we definitely need to know.
	// https://issues.redhat.com/browse/XCMSTRAT-950 for reference when this intentional time bomb explodes.
	if time.Now().Before(Must(time.Parse(time.RFC3339, "2025-09-02T15:04:05Z"))) {
		ret.Insecure = true
	}
	return ret, nil
}

// DeleteHCPCluster deletes an hcp cluster and waits for the operation to complete
func DeleteHCPCluster(
	ctx context.Context,
	hcpClient *hcpapi20240610.HcpOpenShiftClustersClient,
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
	case hcpapi20240610.HcpOpenShiftClustersClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// DeleteResourceGroup deletes a resource group and waits for the operation to complete
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpapi20240610.HcpOpenShiftClustersClient,
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

// VerifyNodePool verifies that a NodePool has the expected configuration.
// This function uses the Kubernetes client to check the HyperShift NodePool CRD in the cluster namespace.
func VerifyNodePool(ctx context.Context, adminRESTConfig *rest.Config, clusterName, nodePoolName string, additionalVerifiers ...NodePoolVerifier) error {
	// Default verifiers that always run
	defaultVerifiers := []NodePoolVerifier{
		verifyNodePoolBasicAccess{clusterName: clusterName, nodePoolName: nodePoolName},
	}

	allVerifiers := append(defaultVerifiers, additionalVerifiers...)

	errs := []error{}
	for _, verifier := range allVerifiers {
		err := verifier.Verify(ctx, adminRESTConfig, clusterName, nodePoolName)
		if err != nil {
			errs = append(errs, fmt.Errorf("%v failed: %w", verifier.Name(), err))
		}
	}

	return errors.Join(errs...)
}

type NodePoolVerifier interface {
	Name() string
	Verify(ctx context.Context, adminRESTConfig *rest.Config, clusterName, nodePoolName string) error
}

// verifyNodePoolBasicAccess verifies basic access to the NodePool resource
type verifyNodePoolBasicAccess struct {
	clusterName  string
	nodePoolName string
}

func (v verifyNodePoolBasicAccess) Name() string {
	return "VerifyNodePoolBasicAccess"
}

func (v verifyNodePoolBasicAccess) Verify(ctx context.Context, adminRESTConfig *rest.Config, clusterName, nodePoolName string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	nodePoolGVR := schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "nodepools",
	}

	namespace := clusterName
	_, err = dynamicClient.Resource(nodePoolGVR).Namespace(namespace).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get NodePool %s in namespace %s: %w", nodePoolName, namespace, err)
	}

	return nil
}

// verifyNodePoolReplicas verifies the expected number of replicas
type verifyNodePoolReplicas struct {
	expectedReplicas int32
}

func (v verifyNodePoolReplicas) Name() string {
	return "VerifyNodePoolReplicas"
}

func (v verifyNodePoolReplicas) Verify(ctx context.Context, adminRESTConfig *rest.Config, clusterName, nodePoolName string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	nodePoolGVR := schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "nodepools",
	}

	namespace := clusterName
	nodePool, err := dynamicClient.Resource(nodePoolGVR).Namespace(namespace).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get NodePool %s in namespace %s: %w", nodePoolName, namespace, err)
	}

	replicas, found, err := unstructured.NestedInt64(nodePool.Object, "spec", "replicas")
	if err != nil {
		return fmt.Errorf("failed to get replicas from NodePool spec: %w", err)
	}
	if !found {
		return fmt.Errorf("replicas not found in NodePool spec")
	}
	if replicas != int64(v.expectedReplicas) {
		return fmt.Errorf("expected %d replicas, got %d", v.expectedReplicas, replicas)
	}

	return nil
}

// verifyNodePoolOsDiskSize verifies the expected OS disk size
type verifyNodePoolOsDiskSize struct {
	expectedOsDiskSizeGiB int32
}

func (v verifyNodePoolOsDiskSize) Name() string {
	return "VerifyNodePoolOsDiskSize"
}

func (v verifyNodePoolOsDiskSize) Verify(ctx context.Context, adminRESTConfig *rest.Config, clusterName, nodePoolName string) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	nodePoolGVR := schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "nodepools",
	}

	namespace := clusterName
	nodePool, err := dynamicClient.Resource(nodePoolGVR).Namespace(namespace).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get NodePool %s in namespace %s: %w", nodePoolName, namespace, err)
	}

	diskSize, found, err := unstructured.NestedInt64(nodePool.Object, "spec", "platform", "azure", "osDisk", "sizeGiB")
	if err != nil {
		return fmt.Errorf("failed to get osDisk.sizeGiB from NodePool spec: %w", err)
	}
	if !found {
		return fmt.Errorf("osDisk.sizeGiB not found in NodePool spec")
	}
	if diskSize != int64(v.expectedOsDiskSizeGiB) {
		return fmt.Errorf("expected osDisk.sizeGiB %d, got %d", v.expectedOsDiskSizeGiB, diskSize)
	}

	return nil
}

// Helper functions to create verifiers with specific parameters
func VerifyNodePoolReplicas(expectedReplicas int32) NodePoolVerifier {
	return verifyNodePoolReplicas{expectedReplicas: expectedReplicas}
}

func VerifyNodePoolOsDiskSize(expectedOsDiskSizeGiB int32) NodePoolVerifier {
	return verifyNodePoolOsDiskSize{expectedOsDiskSizeGiB: expectedOsDiskSizeGiB}
}
