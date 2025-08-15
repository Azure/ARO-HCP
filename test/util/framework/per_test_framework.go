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
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/onsi/ginkgo/v2"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/yaml"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type perItOrDescribeTestContext struct {
	perBinaryInvocationTestContext *perBinaryInvocationTestContext

	contextLock                   sync.RWMutex
	knownResourceGroups           []string
	subscriptionID                string
	clientFactory20240610         *hcpapi20240610.ClientFactory
	armResourcesClientFactory     *armresources.ClientFactory
	armSubscriptionsClientFactory *armsubscriptions.ClientFactory
}

func NewTestContext() *perItOrDescribeTestContext {
	tc := &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: invocationContext(),
	}

	// this construct allows us to be called before or after the test has started and still properly register cleanup.
	if !ginkgo.GetSuite().InRunPhase() {
		ginkgo.BeforeEach(tc.BeforeEach, AnnotatedLocation("set up test context"))
	} else {
		tc.BeforeEach(context.TODO())
	}

	return tc
}

// BeforeEach gives a chance for initialization (none yet) and registers the cleanup
func (tc *perItOrDescribeTestContext) BeforeEach(ctx context.Context) {
	// DeferCleanup, in contrast to AfterEach, triggers execution in
	// first-in-last-out order. This ensures that the framework instance
	// remains valid as long as possible.
	//
	// In addition, AfterEach will not be called if a test never gets here.
	// We are doing this because there's a serious bug.  I haven't got an ETA on a fix, but if we fail to correct it, we definitely need to know.
	// If we haven't fixed the deletion timeout after Labor Day, this intentional time bomb will explode and start failing again.
	cleanupTimeout := 45 * time.Minute
	if time.Now().Before(Must(time.Parse(time.RFC3339, "2025-09-02T15:04:05Z"))) {
		cleanupTimeout = 90 * time.Minute
	}
	ginkgo.DeferCleanup(tc.deleteCreatedResources, AnnotatedLocation("tear down test context"), ginkgo.NodeTimeout(cleanupTimeout))

	// Registered later and thus runs before deleting namespaces.
	ginkgo.DeferCleanup(tc.collectDebugInfo, AnnotatedLocation("dump debug info"), ginkgo.NodeTimeout(45*time.Minute))
}

// deleteCreatedResources deletes what was created that we know of.
func (tc *perItOrDescribeTestContext) deleteCreatedResources(ctx context.Context) {
	tc.contextLock.RLock()
	defer tc.contextLock.RUnlock()
	ginkgo.GinkgoLogr.Info("deleting created resources")

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, resourceGroupName := range tc.knownResourceGroups {
		currResourceGroupName := resourceGroupName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return tc.cleanupResourceGroup(ctx, currResourceGroupName)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		ginkgo.GinkgoLogr.Error(err, "at least one resource group failed to delete: %w", err)
	}

	ginkgo.GinkgoLogr.Info("finished deleting created resources")
}

// collectDebugInfo collects information and saves it in artifact dir
func (tc *perItOrDescribeTestContext) collectDebugInfo(ctx context.Context) {
	tc.contextLock.RLock()
	defer tc.contextLock.RUnlock()
	ginkgo.GinkgoLogr.Info("collecting debug info")

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, resourceGroupName := range tc.knownResourceGroups {
		currResourceGroupName := resourceGroupName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return tc.collectDebugInfoForResourceGroup(ctx, currResourceGroupName)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		ginkgo.GinkgoLogr.Error(err, "at least one resource group failed to collect: %w", err)
	}

	ginkgo.GinkgoLogr.Info("finished collecting debug info")
}

func (tc *perItOrDescribeTestContext) NewResourceGroup(ctx context.Context, resourceGroupPrefix, location string) (*armresources.ResourceGroup, error) {
	suffix := rand.String(6)
	resourceGroupName := SuffixName(resourceGroupPrefix, suffix, 64)
	func() {
		tc.contextLock.Lock()
		defer tc.contextLock.Unlock()
		tc.knownResourceGroups = append(tc.knownResourceGroups, resourceGroupName)
	}()
	ginkgo.GinkgoLogr.Info("creating resource group", "resourceGroup", resourceGroupName)

	if len(tc.perBinaryInvocationTestContext.sharedDir) > 0 {
		resourceGroupCleanupFilename := filepath.Join(tc.perBinaryInvocationTestContext.sharedDir, "tracked-resource-group_"+resourceGroupName)
		if err := os.WriteFile(resourceGroupCleanupFilename, []byte{}, 0644); err != nil {
			ginkgo.GinkgoLogr.Error(err, "failed writing resource group cleanup file", "resourceGroup", resourceGroupName)
		}
	}

	resourceGroup, err := CreateResourceGroup(ctx, tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient(), resourceGroupName, location, 20*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group: %w", err)
	}

	return resourceGroup, nil
}

// cleanupResourceGroup is the standard resourcegroup cleanup.  It attempts to
// 1. delete all HCP clusters and wait for success
// 2. delete the resource group and wait for success
func (tc *perItOrDescribeTestContext) cleanupResourceGroup(ctx context.Context, resourceGroupName string) error {
	errs := []error{}

	ginkgo.GinkgoLogr.Info("deleting all hcp clusters in resource group", "resourceGroup", resourceGroupName)
	if hcpClientFactory, err := tc.get20240610ClientFactoryUnlocked(ctx); err == nil {
		err := DeleteAllHCPClusters(ctx, hcpClientFactory.NewHcpOpenShiftClustersClient(), resourceGroupName, 60*time.Minute)
		if err != nil {
			return fmt.Errorf("failed to cleanup resource group: %w", err)
		}
	} else {
		errs = append(errs, fmt.Errorf("failed creating client factory for cleanup: %w", err))
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if armClientFactory, err := tc.getARMResourcesClientFactoryUnlocked(ctx); err == nil {
		err := DeleteResourceGroup(ctx, armClientFactory.NewResourceGroupsClient(), resourceGroupName, 60*time.Minute)
		if err != nil {
			return fmt.Errorf("failed to cleanup resource group: %w", err)
		}
	} else {
		errs = append(errs, fmt.Errorf("failed creating client factory for cleanup: %w", err))
	}

	return errors.Join(errs...)
}

func (tc *perItOrDescribeTestContext) collectDebugInfoForResourceGroup(ctx context.Context, resourceGroupName string) error {
	// TODO shift to only collect failed once we're confident it runs and works.
	//if !ginkgo.CurrentSpecReport().Failed() {
	//	// only collect data if we failed
	//	return nil
	//}

	errs := []error{}

	armResourceClient, err := tc.getARMResourcesClientFactoryUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ARM resource client: %w", err)
	}

	ginkgo.GinkgoLogr.Info("collecting deployments", "resourceGroup", resourceGroupName)
	allDeployments, err := ListAllDeployments(ctx, armResourceClient.NewDeploymentsClient(), resourceGroupName, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to list deployments in %q: %w", resourceGroupName, err)
	}
	fullDeploymentListPath := filepath.Join(tc.perBinaryInvocationTestContext.artifactDir, "resourcegroups", resourceGroupName, "deployments.yaml")
	if err := os.MkdirAll(filepath.Dir(fullDeploymentListPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", filepath.Dir(fullDeploymentListPath), err)
	}
	allDeploymentsYaml, err := yaml.Marshal(allDeployments)
	if err != nil {
		return fmt.Errorf("failed to marshal deployments: %w", err)
	}
	if err := os.WriteFile(fullDeploymentListPath, allDeploymentsYaml, 0644); err != nil {
		return fmt.Errorf("failed to write deployments to %q: %w", fullDeploymentListPath, err)
	}

	for _, deployment := range allDeployments {
		ginkgo.GinkgoLogr.Info("collecting operations", "resourceGroup", resourceGroupName, "deployment", *deployment.Name)
		allOperations, err := ListAllOperations(ctx, armResourceClient.NewDeploymentOperationsClient(), resourceGroupName, *deployment.Name, 10*time.Minute)
		if err != nil {
			return fmt.Errorf("failed to list operations in %q: %w", *deployment.Name, err)
		}
		fullOperationsListPath := filepath.Join(tc.perBinaryInvocationTestContext.artifactDir, "resourcegroups", resourceGroupName, fmt.Sprintf("deployment-operations-%s.yaml", *deployment.Name))
		if err := os.MkdirAll(filepath.Dir(fullOperationsListPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", filepath.Dir(fullOperationsListPath), err)
		}
		allOperationsYaml, err := yaml.Marshal(allOperations)
		if err != nil {
			return fmt.Errorf("failed to marshal operations: %w", err)
		}
		if err := os.WriteFile(fullOperationsListPath, allOperationsYaml, 0644); err != nil {
			return fmt.Errorf("failed to write operations to %q: %w", fullOperationsListPath, err)
		}
	}

	return errors.Join(errs...)
}

func (tc *perItOrDescribeTestContext) GetARMResourcesClientFactoryOrDie(ctx context.Context) *armresources.ClientFactory {
	return Must(tc.GetARMResourcesClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) Get20240610ClientFactoryOrDie(ctx context.Context) *hcpapi20240610.ClientFactory {
	return Must(tc.Get20240610ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) GetARMSubscriptionsClientFactory() (*armsubscriptions.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20240610 != nil {
		defer tc.contextLock.RUnlock()
		return tc.armSubscriptionsClientFactory, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.getARMSubscriptionsClientFactoryUnlocked()
}

func (tc *perItOrDescribeTestContext) getARMSubscriptionsClientFactoryUnlocked() (*armsubscriptions.ClientFactory, error) {
	if tc.armResourcesClientFactory != nil {
		return tc.armSubscriptionsClientFactory, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	clientFactory, err := armsubscriptions.NewClientFactory(creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.armSubscriptionsClientFactory = clientFactory

	return tc.armSubscriptionsClientFactory, nil
}

func (tc *perItOrDescribeTestContext) GetARMResourcesClientFactory(ctx context.Context) (*armresources.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20240610 != nil {
		defer tc.contextLock.RUnlock()
		return tc.armResourcesClientFactory, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.getARMResourcesClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) getARMResourcesClientFactoryUnlocked(ctx context.Context) (*armresources.ClientFactory, error) {
	if tc.armResourcesClientFactory != nil {
		return tc.armResourcesClientFactory, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := armresources.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.armResourcesClientFactory = clientFactory

	return tc.armResourcesClientFactory, nil
}

func (tc *perItOrDescribeTestContext) Get20240610ClientFactory(ctx context.Context) (*hcpapi20240610.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20240610 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20240610, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20240610ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) get20240610ClientFactoryUnlocked(ctx context.Context) (*hcpapi20240610.ClientFactory, error) {
	if tc.clientFactory20240610 != nil {
		return tc.clientFactory20240610, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpapi20240610.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20240610 = clientFactory

	return tc.clientFactory20240610, nil
}

func (tc *perItOrDescribeTestContext) getSubscriptionIDUnlocked(ctx context.Context) (string, error) {
	if len(tc.subscriptionID) > 0 {
		return tc.subscriptionID, nil
	}

	clientFactory, err := tc.getARMSubscriptionsClientFactoryUnlocked()
	if err != nil {
		return "", fmt.Errorf("failed to get ARM subscriptions client factory: %w", err)
	}

	return tc.perBinaryInvocationTestContext.getSubscriptionID(ctx, clientFactory.NewClient())
}

func (tc *perItOrDescribeTestContext) Location() string {
	return tc.perBinaryInvocationTestContext.Location()
}
