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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

type perItOrDescribeTestContext struct {
	perBinaryInvocationTestContext *perBinaryInvocationTestContext

	contextLock                   sync.RWMutex
	knownResourceGroups           []string
	knownAppRegistrationIDs       []string
	subscriptionID                string
	clientFactory20240610         *hcpsdk20240610preview.ClientFactory
	armComputeClientFactory       *armcompute.ClientFactory
	armResourcesClientFactory     *armresources.ClientFactory
	armSubscriptionsClientFactory *armsubscriptions.ClientFactory
	armNetworkClientFactory       *armnetwork.ClientFactory
	graphClient                   *graphutil.Client

	azureLogFile     *os.File
	timingMetadata   timing.SpecTimingMetadata
	knownDeployments []deploymentInfo
}

type deploymentInfo struct {
	resourceGroupName string
	deploymentName    string
}

func setupAzureLogging(artifactDir string) *os.File {
	if len(artifactDir) == 0 {
		return nil
	}

	// Set up Azure SDK logging to a file so it doesn't pollute test output but is
	// available for debugging. The log file is written to ${ARTIFACT_DIR}/<test-name>/azure.log.
	report := ginkgo.CurrentSpecReport()
	testName := sanitizeTestName(append(report.ContainerHierarchyTexts, report.LeafNodeText))
	logDir := filepath.Join(artifactDir, testName)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to create azure log file")
		return nil
	}

	azureLogFile, err := os.Create(filepath.Join(logDir, "azure.log"))
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to create azure log file")
		return nil
	}

	azureLogger := logr.FromSlogHandler(slog.NewJSONHandler(azureLogFile, &slog.HandlerOptions{}))
	log.SetListener(func(event log.Event, msg string) {
		azureLogger.Info(msg, "event", event)
	})
	// There are other options to log, but they are really noisy.  If we must we can enable them.
	log.SetEvents(log.EventRequest, log.EventResponse, log.EventResponseError, log.EventRetryPolicy, log.EventLRO)

	return azureLogFile
}

func NewTestContext() *perItOrDescribeTestContext {
	azureLogFile := setupAzureLogging(artifactDir())

	tc := &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: invocationContext(),
		azureLogFile:                   azureLogFile,
		timingMetadata: timing.SpecTimingMetadata{
			// Answering the question of "what's the currently-running test name?" in Ginkgo is difficult -
			// all we know in general is the hierarchy of nodes under which we are currently running. We
			// need to have some stable identifier for this test context to record metadata in the global,
			// but we do not want metadata registration to be sensitive to a test author choosing to nest
			// another `By()` node or whatever, so we can snapshot the hierarchy at test context construction
			// time to keep a record of the "root" name for registration purposes.
			//
			// n.b. ContainerHierarchyTexts contains Describe() and Context() but does not contain It(),
			// and the LeadNodeText has It() but not By(), so the full identifier must contain both the
			// hierarchy prefix and the leaf node. Multiple tests nested in By() under one It() will run
			// afoul of this approach, but that looks to never happen based on convention.
			Identifier:  append(ginkgo.CurrentSpecReport().ContainerHierarchyTexts, ginkgo.CurrentSpecReport().LeafNodeText),
			StartedAt:   time.Now().Format(time.RFC3339),
			Steps:       make([]timing.StepTimingMetadata, 0),
			Deployments: make(map[string]map[string][]timing.Operation),
		},
	}

	// this construct allows us to be called before or after the test has started and still properly register cleanup.
	if !ginkgo.GetSuite().InRunPhase() {
		ginkgo.BeforeEach(tc.BeforeEach, AnnotatedLocation("set up test context"))
	} else {
		tc.BeforeEach(context.TODO())
	}

	return tc
}

// sanitizeTestName creates a filesystem-safe directory name from a test's hierarchy of texts.
func sanitizeTestName(parts []string) string {
	name := strings.Join(parts, "_")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		if r == ' ' {
			return '_'
		}
		return r
	}, name)
	// Truncate to a reasonable length for filesystem paths
	if len(name) > 200 {
		name = name[:200]
	}
	return name
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

	ginkgo.DeferCleanup(tc.commitTimingMetadata, AnnotatedLocation("dump timing info"), ginkgo.NodeTimeout(45*time.Minute))

	ginkgo.DeferCleanup(tc.closeAzureLogFile, AnnotatedLocation("close azure log file"))
}

func (tc *perItOrDescribeTestContext) closeAzureLogFile() {
	if tc.azureLogFile != nil {
		tc.azureLogFile.Close()
	}
}

// deleteCreatedResources deletes what was created that we know of.
func (tc *perItOrDescribeTestContext) deleteCreatedResources(ctx context.Context) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Delete created resources", startTime, finishTime)
	}()

	if tc.perBinaryInvocationTestContext.skipCleanup {
		ginkgo.GinkgoLogr.Info("skipping resource cleanup")
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
	tc.contextLock.RUnlock()
	ginkgo.GinkgoLogr.Info("deleting created resources")

	opts := CleanupResourceGroupsOptions{
		ResourceGroupNames: resourceGroupNames,
		Timeout:            60 * time.Minute,
		CleanupWorkflow:    CleanupWorkflowStandard,
	}
	errCleanupResourceGroups := tc.CleanupResourceGroups(ctx, opts)
	if errCleanupResourceGroups != nil {
		ginkgo.GinkgoLogr.Error(errCleanupResourceGroups, "at least one resource group failed to delete")
	}

	err = CleanupAppRegistrations(ctx, graphClient, appRegistrations)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "at least one app registration failed to delete")
	}

	if err := tc.releaseLeasedIdentities(ctx); err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to release leased identities")
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

type CleanupWorkflow string

const (
	CleanupWorkflowStandard CleanupWorkflow = "standard"
	CleanupWorkflowNoRP     CleanupWorkflow = "no-rp"
)

type CleanupResourceGroupsOptions struct {
	ResourceGroupNames []string
	Timeout            time.Duration
	CleanupWorkflow    CleanupWorkflow
}

func (tc *perItOrDescribeTestContext) CleanupResourceGroups(ctx context.Context, opts CleanupResourceGroupsOptions) error {
	// deletion takes a while, it's worth it to do this in parallel
	wg := sync.WaitGroup{}
	errCh := make(chan error, len(opts.ResourceGroupNames))
	for _, currResourceGroupName := range opts.ResourceGroupNames {
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			switch opts.CleanupWorkflow {
			case CleanupWorkflowStandard:
				if err := tc.cleanupResourceGroup(ctx, currResourceGroupName, opts.Timeout); err != nil {
					errCh <- err
				}
			case CleanupWorkflowNoRP:
				if err := tc.cleanupResourceGroupNoRP(ctx, currResourceGroupName, opts.Timeout); err != nil {
					errCh <- err
				}
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
	ginkgo.GinkgoLogr.Info("collecting debug info")

	leasedContainers, err := tc.leasedIdentityContainers()
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to get leased identity containers")
		return
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	tc.contextLock.RLock()
	resourceGroups := append(
		append([]string(nil), tc.knownResourceGroups...),
		leasedContainers...,
	)
	tc.contextLock.RUnlock()
	for _, resourceGroupName := range resourceGroups {
		currResourceGroupName := resourceGroupName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return tc.collectDebugInfoForResourceGroup(ctx, currResourceGroupName)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		ginkgo.GinkgoLogr.Error(err, "at least one resource group failed to collect")
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

func (tc *perItOrDescribeTestContext) findManagedResourceGroups(ctx context.Context, ResourceGroupName string) ([]string, error) {
	managedResourceGroups := []string{}
	clientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return nil, err
	}

	pager := clientFactory.NewResourceGroupsClient().NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resource groups while discovering managed groups: %w", err)
		}
		for _, rg := range page.Value {
			if rg.ManagedBy == nil {
				continue
			}

			// Match managed resource groups whose owner is an HCP resource in the parent resource group
			if strings.Contains(
				strings.ToLower(*rg.ManagedBy),
				strings.ToLower("/resourceGroups/"+ResourceGroupName+"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/"),
			) {
				managedResourceGroups = append(managedResourceGroups, *rg.Name)
			}
		}
	}
	return managedResourceGroups, nil
}

// cleanupResourceGroup is the standard resourcegroup cleanup.  It attempts to
// 1. delete all HCP clusters and wait for success
// 2. check if any managed resource groups are left behind
// 3. delete the resource group and wait for success
func (tc *perItOrDescribeTestContext) cleanupResourceGroup(ctx context.Context, resourceGroupName string, timeout time.Duration) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Clean up resource group %s", resourceGroupName), startTime, finishTime)
	}()

	resourceClientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return err
	}

	networkClientFactory, err := tc.GetARMNetworkClientFactory(ctx)
	if err != nil {
		return err
	}

	hcpClientFactory, err := tc.Get20240610ClientFactory(ctx)
	if err != nil {
		return err
	}

	ginkgo.GinkgoLogr.Info("deleting all hcp clusters in resource group", "resourceGroup", resourceGroupName)
	if err := DeleteAllHCPClusters(ctx, hcpClientFactory.NewHcpOpenShiftClustersClient(), resourceGroupName, timeout); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
	}

	managedResourceGroups, err := tc.findManagedResourceGroups(ctx, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to search for managed resource groups: %w", err)
	}

	if len(managedResourceGroups) > 0 {
		return fmt.Errorf("found %d managed resource groups left behind HCP clusters in %s", len(managedResourceGroups), resourceGroupName)
	} else {
		ginkgo.GinkgoLogr.Info("no left behind managed resource groups found", "resourceGroup", resourceGroupName)
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if err := DeleteResourceGroup(ctx, resourceClientFactory.NewResourceGroupsClient(), networkClientFactory, resourceGroupName, false, timeout); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
	}

	return nil
}

// cleanupResourceGroupNoRP performs cleanup when the resource provider is not available.
// This is used to cleanup personal dev e2e test runs, where the infra is already gone so there's no
// RP to call for HCP deletion.
//  1. discovers any "managed" resource groups whose ManagedBy references a resource in the parent
//     resource group and deletes them (using 'force' to speed up VM/VMSS deletion).
//  2. deletes the parent resource group itself.
func (tc *perItOrDescribeTestContext) cleanupResourceGroupNoRP(ctx context.Context, resourceGroupName string, timeout time.Duration) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.recordTestStepUnlocked(fmt.Sprintf("Clean up resource group %s (no RP)", resourceGroupName), startTime, finishTime)
	}()

	errs := []error{}

	managedResourceGroups, err := tc.findManagedResourceGroups(ctx, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to search for managed resource groups: %w", err)
	}

	resourceClientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return err
	}

	networkClientFactory, err := tc.GetARMNetworkClientFactory(ctx)
	if err != nil {
		return err
	}

	for _, managedRG := range managedResourceGroups {
		ginkgo.GinkgoLogr.Info("deleting managed resource group", "resourceGroup", managedRG, "parentResourceGroup", resourceGroupName)
		if err := DeleteResourceGroup(ctx, resourceClientFactory.NewResourceGroupsClient(), networkClientFactory, managedRG, true, timeout); err != nil {
			if isIgnorableResourceGroupCleanupError(err) {
				ginkgo.GinkgoLogr.Info("ignoring not found resource group", "resourceGroup", managedRG)
			} else {
				return fmt.Errorf("failed to cleanup managed resource group %q: %w", managedRG, err)
			}
		}
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if err := DeleteResourceGroup(ctx, resourceClientFactory.NewResourceGroupsClient(), networkClientFactory, resourceGroupName, false, timeout); err != nil {
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

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Collect debug info for resource group %s", resourceGroupName), startTime, finishTime)
	}()

	errs := []error{}

	armResourceClient, err := tc.GetARMResourcesClientFactory(ctx)
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

func (tc *perItOrDescribeTestContext) GetARMComputeClientFactoryOrDie(ctx context.Context) *armcompute.ClientFactory {
	return Must(tc.GetARMComputeClientFactory(ctx))
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

func (tc *perItOrDescribeTestContext) GetARMNetworkClientFactory(ctx context.Context) (*armnetwork.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.armNetworkClientFactory != nil {
		defer tc.contextLock.RUnlock()
		return tc.armNetworkClientFactory, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.getARMNetworkClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) getARMNetworkClientFactoryUnlocked(ctx context.Context) (*armnetwork.ClientFactory, error) {
	if tc.armNetworkClientFactory != nil {
		return tc.armNetworkClientFactory, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}

	// We already hold the lock, so we call the unlocked version
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}

	clientFactory, err := armnetwork.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.armNetworkClientFactory = clientFactory
	return tc.armNetworkClientFactory, nil
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

func (tc *perItOrDescribeTestContext) GetARMComputeClientFactory(ctx context.Context) (*armcompute.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.armComputeClientFactory != nil {
		defer tc.contextLock.RUnlock()
		return tc.armComputeClientFactory, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.getARMComputeClientFactoryUnlocked(ctx)
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

func (tc *perItOrDescribeTestContext) getARMComputeClientFactoryUnlocked(ctx context.Context) (*armcompute.ClientFactory, error) {
	if tc.armComputeClientFactory != nil {
		return tc.armComputeClientFactory, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := armcompute.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.armComputeClientFactory = clientFactory

	return tc.armComputeClientFactory, nil
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

func (tc *perItOrDescribeTestContext) PullSecretPath() string {
	return tc.perBinaryInvocationTestContext.pullSecretPath
}

func (tc *perItOrDescribeTestContext) FindVirtualMachineSizeMatching(ctx context.Context, pattern *regexp.Regexp) (string, error) {
	if pattern == nil {
		return "", fmt.Errorf("pattern cannot be nil")
	}

	clientFactory, err := tc.GetARMComputeClientFactory(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get ARM compute client factory: %w", err)
	}

	location := tc.Location()
	matches := make([]string, 0)

	vmSizesClient := clientFactory.NewVirtualMachineSizesClient()
	pager := vmSizesClient.NewListPager(location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list VM sizes in %s: %w", location, err)
		}
		if page.Value == nil {
			continue
		}
		for _, size := range page.Value {
			if size.Name == nil {
				continue
			}
			if pattern.MatchString(*size.Name) {
				matches = append(matches, *size.Name)
			}
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no VM size matching %q found in %s", pattern.String(), location)
	}

	// Randomly select a VM size from the matches to avoid bias towards the first or last size in the list.
	selected := matches[rand.Intn(len(matches))]
	return selected, nil
}

func (tc *perItOrDescribeTestContext) SubscriptionID(ctx context.Context) (string, error) {
	tc.contextLock.Lock()
	if len(tc.subscriptionID) > 0 {
		defer tc.contextLock.RUnlock()
		return tc.subscriptionID, nil
	}
	tc.contextLock.Unlock()

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

func (tc *perItOrDescribeTestContext) recordDeploymentOperationsUnlocked(resourceGroup, deployment string, operations []timing.Operation) {
	if _, exists := tc.timingMetadata.Deployments[resourceGroup]; !exists {
		tc.timingMetadata.Deployments[resourceGroup] = make(map[string][]timing.Operation)
	}
	tc.timingMetadata.Deployments[resourceGroup][deployment] = operations
}

func (tc *perItOrDescribeTestContext) RecordKnownDeployment(resourceGroup, deployment string) {
	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()
	tc.knownDeployments = append(tc.knownDeployments, deploymentInfo{
		resourceGroupName: resourceGroup,
		deploymentName:    deployment,
	})
}

func (tc *perItOrDescribeTestContext) RecordTestStep(name string, startTime, finishTime time.Time) {
	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	tc.recordTestStepUnlocked(name, startTime, finishTime)
}

func (tc *perItOrDescribeTestContext) recordTestStepUnlocked(name string, startTime, finishTime time.Time) {
	tc.timingMetadata.Steps = append(tc.timingMetadata.Steps, timing.StepTimingMetadata{
		Name:       name,
		StartedAt:  startTime.Format(time.RFC3339),
		FinishedAt: finishTime.Format(time.RFC3339),
	})
}

func (tc *perItOrDescribeTestContext) commitTimingMetadata(ctx context.Context) {
	ginkgo.GinkgoLogr.Info("Commiting timing metadata.")

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	factory, err := tc.getARMResourcesClientFactoryUnlocked(ctx)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Failed to get ARM resource client factory: %v", err))
	}
	operationsClient := factory.NewDeploymentOperationsClient()
	for _, info := range tc.knownDeployments {
		resourceGroupName, deploymentName := info.resourceGroupName, info.deploymentName
		ginkgo.GinkgoLogr.Info("Dumping deployment operations.", "deployment", deploymentName, "resourceGroup", resourceGroupName)
		operations, err := fetchOperationsFor(ctx, operationsClient, resourceGroupName, deploymentName)
		if err != nil {
			ginkgo.GinkgoLogr.Error(err, "failed to fetch operations for deployment", "deployment", deploymentName, "resourceGroup", resourceGroupName)
			continue
		}
		tc.recordDeploymentOperationsUnlocked(resourceGroupName, deploymentName, operations)
	}

	tc.timingMetadata.FinishedAt = time.Now().Format(time.RFC3339)
	encoded, err := yaml.Marshal(tc.timingMetadata)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "Failed to marshal timing metadata")
		return
	}

	encodedIdentifier, err := yaml.Marshal(tc.timingMetadata.Identifier)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "Failed to marshal timing identifier")
		return
	}
	hash := sha256.New()
	hash.Write(encodedIdentifier)
	hashBytes := hash.Sum(nil)
	for _, dir := range []string{tc.perBinaryInvocationTestContext.sharedDir, filepath.Join(tc.perBinaryInvocationTestContext.artifactDir, "test-timing")} {
		output := filepath.Join(dir, fmt.Sprintf("timing-metadata-%s.yaml", hex.EncodeToString(hashBytes)))
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to create directory for timing metadata")
			continue
		}
		if err := os.WriteFile(output, encoded, 0644); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to write timing metadata")
			continue
		}

		ginkgo.GinkgoLogr.Info("Wrote timing metadata", "path", output)
	}
}
