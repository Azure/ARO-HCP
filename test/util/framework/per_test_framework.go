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
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type perItOrDescribeTestContext struct {
	perBinaryInvocationTestContext *perBinaryInvocationTestContext

	contextLock                   sync.RWMutex
	knownResourceGroups           []string
	knownAppRegistrationIDs       []string
	subscriptionID                string
	clientFactory20240610         *hcpsdk20240610preview.ClientFactory
	armResourcesClientFactory     *armresources.ClientFactory
	armSubscriptionsClientFactory *armsubscriptions.ClientFactory
	graphClient                   *graphutil.Client
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
	if tc.perBinaryInvocationTestContext.skipCleanup {
		ginkgo.GinkgoLogr.Info("skipping resource cleanup")
		return
	}

	hcpClientFactory, err := tc.Get20240610ClientFactory(ctx)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to get HCP client")
		return
	}
	resourceGroupsClientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to get ARM client")
		return
	}
	graphClient, err := tc.GetGraphClient(ctx)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to get Graph client")
		return
	}

	tc.contextLock.RLock()
	resourceGroupNames := tc.knownResourceGroups
	appRegistrations := tc.knownAppRegistrationIDs
	defer tc.contextLock.RUnlock()
	ginkgo.GinkgoLogr.Info("deleting created resources")

	errCleanupResourceGroups := CleanupResourceGroups(ctx, hcpClientFactory.NewHcpOpenShiftClustersClient(), resourceGroupsClientFactory.NewResourceGroupsClient(), resourceGroupNames)
	if errCleanupResourceGroups != nil {
		ginkgo.GinkgoLogr.Error(errCleanupResourceGroups, "at least one resource group failed to delete: %w", errCleanupResourceGroups)
	}

	err = CleanupAppRegistrations(ctx, graphClient, appRegistrations)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "at least one app registration failed to delete: %w", err)
	}

	ginkgo.GinkgoLogr.Info("finished deleting created resources")
	// Register error to ginkgo reporter to ensure the test fails if any errors occur except for not found resource group or resource.
	if isIgnorableResourceGroupCleanupError(errCleanupResourceGroups) {
		ginkgo.GinkgoLogr.Info("ignoring not found resource group or resource cleanup error")
	} else {
		gomega.Expect(errCleanupResourceGroups).ToNot(gomega.HaveOccurred())
	}
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

func isIgnorableResourceGroupCleanupError(err error) bool {
	if err == nil {
		return false
	}

	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		if responseErr.StatusCode == http.StatusNotFound {
			return true
		}

		switch responseErr.ErrorCode {
		case "ResourceGroupNotFound", "ResourceNotFound":
			return true
		}
	}

	return false
}

func CleanupResourceGroups(ctx context.Context, hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient, resourceGroupsClient *armresources.ResourceGroupsClient, resourceGroupNames []string) error {
	// deletion takes a while, it's worth it to do this in parallel
	wg := sync.WaitGroup{}
	errCh := make(chan error, len(resourceGroupNames))
	for _, currResourceGroupName := range resourceGroupNames {
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			if err := cleanupResourceGroup(ctx, hcpClient, resourceGroupsClient, currResourceGroupName); err != nil {
				errCh <- err
			}
		}(ctx)
	}
	wg.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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

	resourceGroup, err := CreateResourceGroup(ctx, tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient(), resourceGroupName, location, StandardResourceGroupExpiration, 20*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group: %w", err)
	}

	return resourceGroup, nil
}

// cleanupResourceGroup is the standard resourcegroup cleanup.  It attempts to
// 1. delete all HCP clusters and wait for success
// 2. delete the resource group and wait for success
func cleanupResourceGroup(ctx context.Context, hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient, resourceGroupsClient *armresources.ResourceGroupsClient, resourceGroupName string) error {
	errs := []error{}

	ginkgo.GinkgoLogr.Info("deleting all hcp clusters in resource group", "resourceGroup", resourceGroupName)
	if err := DeleteAllHCPClusters(ctx, hcpClient, resourceGroupName, 60*time.Minute); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if err := DeleteResourceGroup(ctx, resourceGroupsClient, resourceGroupName, 60*time.Minute); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
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

func (tc *perItOrDescribeTestContext) NewAppRegistrationWithServicePrincipal(ctx context.Context) (*graphutil.Application, *graphutil.ServicePrincipal, error) {
	appName := fmt.Sprintf("aro-hcp-e2e-%d", rand.Int())
	ginkgo.GinkgoLogr.Info("creating app registration", "appName", appName)

	graphClient, err := tc.GetGraphClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get graph client: %w", err)
	}

	app, err := graphClient.CreateApplication(ctx, appName, []string{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create app registration: %w", err)
	}

	func() {
		tc.contextLock.Lock()
		defer tc.contextLock.Unlock()
		// Track the ObjectIDs as that's what operations are performed against, not AppID
		tc.knownAppRegistrationIDs = append(tc.knownAppRegistrationIDs, app.ID)
	}()

	sp, err := graphClient.CreateServicePrincipal(ctx, app.AppID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create service principal: %w", err)
	}

	return app, sp, nil
}

func CleanupAppRegistrations(ctx context.Context, graphClient *graphutil.Client, appRegistrationIDs []string) error {
	var errs []error
	for _, currAppID := range appRegistrationIDs {
		if err := graphClient.DeleteApplication(ctx, currAppID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (tc *perItOrDescribeTestContext) GetARMResourcesClientFactoryOrDie(ctx context.Context) *armresources.ClientFactory {
	return Must(tc.GetARMResourcesClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) Get20240610ClientFactoryOrDie(ctx context.Context) *hcpsdk20240610preview.ClientFactory {
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
	if tc.armResourcesClientFactory != nil {
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

func (tc *perItOrDescribeTestContext) Get20240610ClientFactory(ctx context.Context) (*hcpsdk20240610preview.ClientFactory, error) {
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

func (tc *perItOrDescribeTestContext) get20240610ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20240610preview.ClientFactory, error) {
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
	clientFactory, err := hcpsdk20240610preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
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

func (tc *perItOrDescribeTestContext) GetGraphClient(ctx context.Context) (*graphutil.Client, error) {
	tc.contextLock.RLock()
	if tc.graphClient != nil {
		defer tc.contextLock.RUnlock()
		return tc.graphClient, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.getGraphClientUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) getGraphClientUnlocked(ctx context.Context) (*graphutil.Client, error) {
	if tc.graphClient != nil {
		return tc.graphClient, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	return graphutil.NewClient(ctx, creds)
}

func (tc *perItOrDescribeTestContext) Location() string {
	return tc.perBinaryInvocationTestContext.Location()
}

func (tc *perItOrDescribeTestContext) SubscriptionID(ctx context.Context) (string, error) {
	tc.contextLock.RLock()
	if len(tc.subscriptionID) > 0 {
		defer tc.contextLock.RUnlock()
		return tc.subscriptionID, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()
	return tc.getSubscriptionIDUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) AzureCredential() (azcore.TokenCredential, error) {
	return tc.perBinaryInvocationTestContext.getAzureCredentials()
}

func (tc *perItOrDescribeTestContext) TenantID() string {
	return tc.perBinaryInvocationTestContext.tenantID
}
