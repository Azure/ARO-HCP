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
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

type perItOrDescribeTestContext struct {
	perBinaryInvocationTestContext *perBinaryInvocationTestContext

	contextLock                   sync.RWMutex
	knownResourceGroups           []string
	knownAppRegistrationIDs       []string
	createdRoleAssignmentIDs      []string
	subscriptionID                string
	clientFactory20240610         *hcpsdk20240610preview.ClientFactory
	clientFactory20251223         *hcpsdk20251223preview.ClientFactory
	clientFactory20260630         *hcpsdk20260630preview.ClientFactory
	armComputeClientFactory       *armcompute.ClientFactory
	armResourcesClientFactory     *armresources.ClientFactory
	armSubscriptionsClientFactory *armsubscriptions.ClientFactory
	armNetworkClientFactory       *armnetwork.ClientFactory
	graphClient                   *graphutil.Client

	LogDirPath       string
	azureLogFile     *os.File
	timingMetadata   timing.SpecTimingMetadata
	knownDeployments []deploymentInfo
	hcpAdminConfigs  map[string]*rest.Config
}

type deploymentInfo struct {
	resourceGroupName string
	deploymentName    string
}

// Create log directory for the test in ${ARTIFACT_DIR}/<test-name>/
func setupTestLogDir(artifactDir string) string {
	if len(artifactDir) == 0 {
		return ""
	}

	report := ginkgo.CurrentSpecReport()
	testName := sanitizeTestName(append(report.ContainerHierarchyTexts, report.LeafNodeText))
	logDirPath := filepath.Join(artifactDir, testName)

	if err := os.MkdirAll(logDirPath, 0755); err != nil {
		ginkgo.GinkgoLogr.Error(err, "failed to create azure log file")
		return ""
	}

	return logDirPath
}

// Set up Azure SDK logging to a file so it doesn't pollute test output but is
// available for debugging. The log file is written to ${ARTIFACT_DIR}/<test-name>/azure.log.
func setupAzureLogging(logDirPath string) *os.File {
	if len(logDirPath) == 0 {
		return nil
	}

	azureLogFile, err := os.Create(filepath.Join(logDirPath, "azure.log"))
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
	logDirPath := setupTestLogDir(artifactDir())
	azureLogFile := setupAzureLogging(logDirPath)

	tc := &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: invocationContext(),
		LogDirPath:                     logDirPath,
		azureLogFile:                   azureLogFile,
		hcpAdminConfigs:                make(map[string]*rest.Config),
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

	// Registered last so it runs first in the FILO cleanup order, marking the
	// boundary between the test body and cleanup.
	ginkgo.DeferCleanup(tc.logTestEndAndCleanupStart, AnnotatedLocation("log test end and cleanup start"))

	ginkgo.GinkgoLogr.Info("===== TEST CASE BEGAN =====")
}

func (tc *perItOrDescribeTestContext) logTestEndAndCleanupStart() {
	report := ginkgo.CurrentSpecReport()
	result := "SUCCESS"
	if report.Failed() {
		result = "FAILURE"
	}
	ginkgo.GinkgoLogr.Info(fmt.Sprintf("===== TEST CASE ENDED: %s =====", result))
	ginkgo.GinkgoLogr.Info("===== CLEANUP BEGAN =====")
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
		if !isIgnorableResourceGroupCleanupError(errCleanupResourceGroups) {
			ginkgo.GinkgoLogr.Error(errCleanupResourceGroups, "at least one resource group failed to delete")
		}
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

// isResourceGroupNotFoundError returns true if the error indicates the resource
// group does not exist (HTTP 404 or ResourceGroupNotFound/ResourceNotFound error
// codes). This is used during cleanup and debug collection to gracefully handle
// resource groups that have already been deleted.
func isResourceGroupNotFoundError(err error) bool {
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

func isIgnorableResourceGroupCleanupError(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !isIgnorableResourceGroupCleanupError(e) {
				return false
			}
		}
		return true
	}

	return isResourceGroupNotFoundError(err)
}

type FPACredentials struct {
	ClientID string
	CertPath string
}

func (c FPACredentials) IsConfigured() bool {
	return c.ClientID != "" && c.CertPath != ""
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
	FPACredentials     FPACredentials
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
			defer utilruntime.HandleCrashWithContext(ctx)

			switch opts.CleanupWorkflow {
			case CleanupWorkflowStandard:
				if err := tc.cleanupResourceGroup(ctx, currResourceGroupName, opts.Timeout); err != nil {
					errCh <- err
				}
			case CleanupWorkflowNoRP:
				if err := tc.cleanupResourceGroupNoRP(ctx, currResourceGroupName, opts.Timeout, opts.FPACredentials); err != nil {
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
			defer utilruntime.HandleCrashWithContext(ctx)

			return tc.collectDebugInfoForResourceGroup(ctx, currResourceGroupName)
		})
	}
	waitGroup.Go(func() error {
		defer utilruntime.HandleCrashWithContext(ctx)
		tc.collectHCPInspectData(ctx)
		return nil
	})
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		if !isResourceGroupNotFoundError(err) {
			ginkgo.GinkgoLogr.Error(err, "at least one resource group failed to collect")
		}
	}

	ginkgo.GinkgoLogr.Info("finished collecting debug info")
}

func (tc *perItOrDescribeTestContext) NewResourceGroup(ctx context.Context, resourceGroupPrefix, location string) (*armresources.ResourceGroup, error) {
	// Use a wide random suffix. Some resources created inside the group derive
	// their globally-unique names deterministically from the resource group id
	// (e.g. the customer Key Vault name in customer-infra.bicep is
	// cust-kv-${uniqueString(resourceGroup().id, ...)}). Because Azure Key Vault
	// soft-delete holds a not-yet-purged name for up to 90 days, a repeated
	// resource-group suffix within that window causes a VaultAlreadyExists
	// collision on the next run. The 12-character suffix is the always-on
	// primary defense (a repeat is highly unlikely across the retention
	// window); the best-effort teardown purge below additionally frees the
	// name immediately whenever the identity is permitted to purge. Both are
	// best-effort layers rather than hard guarantees. (SuffixName only
	// preserves the full suffix while prefix+suffix stays within maxLen; if it
	// ever truncates, effective entropy drops to a 32-bit hash — the short
	// prefixes used here never trigger that path.)
	suffix := rand.String(12)
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
		return nil, fmt.Errorf("failed to create resource group %q: %w", resourceGroupName, err)
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

// waitForManagedResourceGroupsDeletion polls findManagedResourceGroups until no managed resource groups remain for the given parent resource group
// This handles the case where managed RGs are still being deleted (e.g. because the HCP cluster was already in a deleting state prior to cleanup)
// Returns the remaining managed resource groups (empty if all were deleted)
func (tc *perItOrDescribeTestContext) waitForManagedResourceGroupsDeletion(ctx context.Context, resourceGroupName string, timeout time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded waiting for managed resource groups in %s to be deleted", timeout.Minutes(), resourceGroupName))
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			remaining, _ := tc.findManagedResourceGroups(context.Background(), resourceGroupName)
			return remaining, fmt.Errorf("timed out waiting for managed resource groups in %q to be deleted, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), ctx.Err())
		case <-time.After(StandardPollInterval):
		}

		managedResourceGroups, err := tc.findManagedResourceGroups(ctx, resourceGroupName)
		if err != nil {
			return nil, fmt.Errorf("failed to search for managed resource groups while waiting for deletion: %w", err)
		}

		if len(managedResourceGroups) == 0 {
			ginkgo.GinkgoLogr.Info("all managed resource groups deleted",
				"resourceGroup", resourceGroupName)
			return nil, nil
		}

		ginkgo.GinkgoLogr.Info("waiting for managed resource group deletion",
			"resourceGroup", resourceGroupName, "remaining", managedResourceGroups)
	}
}

// cleanupResourceGroup is the standard resourcegroup cleanup.  It attempts to
// 1. delete all HCP clusters via the RP and wait for success
// 2. wait for any managed resource groups to be deleted
// 3. delete the resource group
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

	var nonConformantErr error
	ginkgo.GinkgoLogr.Info("deleting all hcp clusters in resource group", "resourceGroup", resourceGroupName)
	if err := DeleteAllHCPClusters20240610(ctx, hcpClientFactory.NewHcpOpenShiftClustersClient(), resourceGroupName, timeout); err != nil {
		if errors.Is(err, &NonConformingClustersError{}) {
			nonConformantErr = err
		} else if isResourceGroupNotFoundError(err) {
			ginkgo.GinkgoLogr.Info("resource group already deleted, skipping cleanup", "resourceGroup", resourceGroupName)
			return nil
		} else {
			return fmt.Errorf("failed to cleanup resource group: %w", err)
		}
	}

	managedResourceGroups, err := tc.findManagedResourceGroups(ctx, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to search for managed resource groups: %w", err)
	}

	if len(managedResourceGroups) > 0 {
		ginkgo.GinkgoLogr.Info("managed resource groups still present, waiting for deletion",
			"resourceGroup", resourceGroupName, "managedResourceGroups", managedResourceGroups)
		managedResourceGroups, err = tc.waitForManagedResourceGroupsDeletion(ctx, resourceGroupName, 10*time.Minute)
		if err != nil {
			if len(managedResourceGroups) > 0 {
				return fmt.Errorf("found %d managed resource groups left behind HCP clusters in %s: %v: %w", len(managedResourceGroups), resourceGroupName, managedResourceGroups, err)
			}
			return fmt.Errorf("failed waiting for managed resource group deletion in %s: %w", resourceGroupName, err)
		}
	} else {
		ginkgo.GinkgoLogr.Info("no left behind managed resource groups found", "resourceGroup", resourceGroupName)
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if err := DeleteResourceGroup(ctx, resourceClientFactory.NewResourceGroupsClient(), networkClientFactory, resourceGroupName, false, timeout); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
	}

	// Deleting the resource group only soft-deletes any Key Vaults it contained;
	// their globally-unique names stay reserved until purged. Purge them so a
	// later run reusing a colliding vault name does not hit VaultAlreadyExists.
	tc.purgeDeletedKeyVaultsInResourceGroup(ctx, resourceGroupName)

	// we want non-conformant clusters to be visible at the end, without impeding our ability to clean up the resource group
	return nonConformantErr
}

// cleanupResourceGroupNoRP performs cleanup when the resource provider is not available.
// This is used to cleanup personal dev e2e test runs, where the infra is already gone so there's no
// RP to call for HCP deletion.
//  1. discovers any "managed" resource groups whose ManagedBy references a resource in the parent
//     resource group and deletes them (using 'force' to speed up VM/VMSS deletion).
//  2. deletes the parent resource group itself.
func (tc *perItOrDescribeTestContext) cleanupResourceGroupNoRP(ctx context.Context, resourceGroupName string, timeout time.Duration, fpaCredentials FPACredentials) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.recordTestStepUnlocked(fmt.Sprintf("Clean up resource group %s (no RP)", resourceGroupName), startTime, finishTime)
	}()

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

	if fpaCredentials.IsConfigured() {
		ginkgo.GinkgoLogr.Info("deleting any remaining RedHatOpenShift service association links in resource group", "resourceGroup", resourceGroupName)
		if err := tc.deleteRedHatOpenShiftServiceAssociationLinks(ctx, resourceGroupName, fpaCredentials); err != nil {
			return fmt.Errorf("failed to delete RedHatOpenShift service association links in %q: %w", resourceGroupName, err)
		}
	}

	ginkgo.GinkgoLogr.Info("deleting resource group", "resourceGroup", resourceGroupName)
	if err := DeleteResourceGroup(ctx, resourceClientFactory.NewResourceGroupsClient(), networkClientFactory, resourceGroupName, false, timeout); err != nil {
		return fmt.Errorf("failed to cleanup resource group: %w", err)
	}

	// Purge any Key Vaults left soft-deleted by the resource group deletion so
	// their globally-unique names are immediately reusable by later runs.
	tc.purgeDeletedKeyVaultsInResourceGroup(ctx, resourceGroupName)

	return nil
}

// purgeDeletedKeyVaultsInResourceGroup purges any soft-deleted Key Vaults that
// belonged to the given resource group. Deleting a resource group only places
// its vaults into a recoverable (soft-deleted) state, and Azure keeps the
// globally-unique vault name reserved for the soft-delete retention window (up
// to 90 days). Because the e2e customer Key Vault name is derived
// deterministically from the resource group id
// (cust-kv-${uniqueString(resourceGroup().id, ...)}), a not-yet-purged vault
// would cause a VaultAlreadyExists collision on a later run that reuses the
// name. This is best-effort: failures (including a missing
// Microsoft.KeyVault/locations/deletedVaults/purge/action permission) are
// logged but never fail cleanup.
func (tc *perItOrDescribeTestContext) purgeDeletedKeyVaultsInResourceGroup(ctx context.Context, resourceGroupName string) {
	// Bound the whole best-effort purge so a stuck vault purge or Azure
	// control-plane stall cannot hang teardown indefinitely.
	ctx, cancel := context.WithTimeout(ctx, keyVaultPurgeTimeout)
	defer cancel()

	creds, err := tc.AzureCredential()
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "unable to purge soft-deleted key vaults: failed to get azure credentials", "resourceGroup", resourceGroupName)
		return
	}
	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "unable to purge soft-deleted key vaults: failed to get subscription id", "resourceGroup", resourceGroupName)
		return
	}
	vaultsClient, err := armkeyvault.NewVaultsClient(subscriptionID, creds, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "unable to purge soft-deleted key vaults: failed to build key vault client", "resourceGroup", resourceGroupName)
		return
	}

	// Soft-deleted vaults expose their original resource id via VaultID; match
	// the resource group segment case-insensitively.
	rgMarker := strings.ToLower("/resourcegroups/" + resourceGroupName + "/")

	pager := vaultsClient.NewListDeletedPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			ginkgo.GinkgoLogr.Error(err, "unable to list soft-deleted key vaults", "resourceGroup", resourceGroupName)
			return
		}
		for _, deleted := range page.Value {
			if deleted == nil || deleted.Name == nil || deleted.Properties == nil ||
				deleted.Properties.VaultID == nil || deleted.Properties.Location == nil {
				continue
			}
			if !strings.Contains(strings.ToLower(*deleted.Properties.VaultID), rgMarker) {
				continue
			}
			ginkgo.GinkgoLogr.Info("purging soft-deleted key vault",
				"keyVault", *deleted.Name, "location", *deleted.Properties.Location, "resourceGroup", resourceGroupName)
			poller, err := vaultsClient.BeginPurgeDeleted(ctx, *deleted.Name, *deleted.Properties.Location, nil)
			if err != nil {
				// A 404 means the vault was already purged or its soft-delete
				// window expired between the list and the purge; that is the
				// desired end state, so treat it as a no-op rather than noise.
				if isKeyVaultNotFound(err) {
					continue
				}
				ginkgo.GinkgoLogr.Error(err, "failed to start purge of soft-deleted key vault; a colliding name may block a later run until it is purged or expires",
					"keyVault", *deleted.Name, "resourceGroup", resourceGroupName)
				continue
			}
			if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval}); err != nil {
				if isKeyVaultNotFound(err) {
					continue
				}
				ginkgo.GinkgoLogr.Error(err, "failed to purge soft-deleted key vault; a colliding name may block a later run until it is purged or expires",
					"keyVault", *deleted.Name, "resourceGroup", resourceGroupName)
			}
		}
	}
}

// isKeyVaultNotFound reports whether err is an Azure 404 response, which for a
// purge means the vault is already gone (already purged or soft-delete window
// expired) and can be treated as a successful no-op.
func isKeyVaultNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
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
		if isResourceGroupNotFoundError(err) {
			ginkgo.GinkgoLogr.Info("resource group not found, skipping debug info collection", "resourceGroup", resourceGroupName)
			return nil
		}
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

func (tc *perItOrDescribeTestContext) collectHCPInspectData(ctx context.Context) {
	if tc.LogDirPath == "" {
		return
	}

	if _, err := exec.LookPath("oc"); err != nil {
		ginkgo.GinkgoLogr.Info("oc not found in PATH, skipping HCP inspect data collection")
		return
	}

	tc.contextLock.RLock()
	configs := make(map[string]*rest.Config, len(tc.hcpAdminConfigs))
	for k, v := range tc.hcpAdminConfigs {
		configs[k] = v
	}
	tc.contextLock.RUnlock()

	if len(configs) == 0 {
		return
	}

	inspectGroup, inspectCtx := errgroup.WithContext(ctx)
	for clusterKey, restConfig := range configs {
		currKey := clusterKey
		currConfig := restConfig
		inspectGroup.Go(func() error {
			defer utilruntime.HandleCrashWithContext(inspectCtx)
			tc.runOCAdmInspect(inspectCtx, currKey, currConfig)
			return nil
		})
	}
	_ = inspectGroup.Wait()
}

func (tc *perItOrDescribeTestContext) runOCAdmInspect(ctx context.Context, clusterKey string, restConfig *rest.Config) {
	parts := strings.SplitN(clusterKey, "/", 2)
	if len(parts) != 2 {
		ginkgo.GinkgoLogr.Error(fmt.Errorf("invalid cluster key %q", clusterKey), "skipping oc adm inspect")
		return
	}
	clusterName := parts[1]
	logger := ginkgo.GinkgoLogr.WithValues("cluster", clusterKey)
	logger.Info("starting oc adm inspect for HCP cluster")

	startTime := time.Now()
	defer func() {
		tc.RecordTestStep(fmt.Sprintf("oc adm inspect %s", clusterKey), startTime, time.Now())
	}()

	kubeconfigContent, err := GenerateKubeconfig(restConfig)
	if err != nil {
		logger.Error(err, "failed to generate kubeconfig for HCP inspect, skipping")
		return
	}

	kubeconfigFile, err := os.CreateTemp("", fmt.Sprintf("inspect-kubeconfig-%s-*.yaml", clusterName))
	if err != nil {
		logger.Error(err, "failed to create temp kubeconfig file, skipping")
		return
	}
	defer os.Remove(kubeconfigFile.Name())
	defer kubeconfigFile.Close()

	if _, err := kubeconfigFile.WriteString(kubeconfigContent); err != nil {
		logger.Error(err, "failed to write temp kubeconfig file, skipping")
		return
	}
	if err := kubeconfigFile.Close(); err != nil {
		logger.Error(err, "failed to flush temp kubeconfig file, skipping")
		return
	}

	inspectDir := filepath.Join(tc.LogDirPath, fmt.Sprintf("inspect-%s", clusterName))

	inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(inspectCtx, "oc", "adm", "inspect",
		"--kubeconfig", kubeconfigFile.Name(),
		"--dest-dir", inspectDir,
		"ns/openshift-ingress",
		"ns/openshift-ingress-operator",
		"clusteroperator/ingress",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error(err, "oc adm inspect failed", "output", string(output))
		if mkdirErr := os.MkdirAll(inspectDir, 0755); mkdirErr == nil {
			_ = os.WriteFile(filepath.Join(inspectDir, "inspect-error.log"), output, 0644)
		}
		return
	}

	logger.Info("oc adm inspect completed successfully", "outputDir", inspectDir)
}

func (tc *perItOrDescribeTestContext) NewAppRegistrationWithServicePrincipal(ctx context.Context) (*graphutil.Application, *graphutil.ServicePrincipal, error) {
	appName := fmt.Sprintf("%s%d", graphutil.AppRegistrationPrefix, rand.Int())
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

func (tc *perItOrDescribeTestContext) Get20251223ClientFactory(ctx context.Context) (*hcpsdk20251223preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20251223 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20251223, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20251223ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) Get20251223ClientFactoryOrDie(ctx context.Context) *hcpsdk20251223preview.ClientFactory {
	return Must(tc.Get20251223ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) get20251223ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20251223preview.ClientFactory, error) {
	if tc.clientFactory20251223 != nil {
		return tc.clientFactory20251223, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20251223preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20251223 = clientFactory

	return tc.clientFactory20251223, nil
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

func (tc *perItOrDescribeTestContext) Get20260630ClientFactory(ctx context.Context) (*hcpsdk20260630preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20260630 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20260630, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20260630ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) Get20260630ClientFactoryOrDie(ctx context.Context) *hcpsdk20260630preview.ClientFactory {
	return Must(tc.Get20260630ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) get20260630ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20260630preview.ClientFactory, error) {
	if tc.clientFactory20260630 != nil {
		return tc.clientFactory20260630, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20260630preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20260630 = clientFactory

	return tc.clientFactory20260630, nil
}
func (tc *perItOrDescribeTestContext) Location() string {
	return tc.perBinaryInvocationTestContext.Location()
}

func (tc *perItOrDescribeTestContext) PullSecretPath() string {
	return tc.perBinaryInvocationTestContext.pullSecretPath
}

// AvailableZones returns the sorted list of non-restricted availability zones
// for the given VM SKU in the current test location, querying the Azure Resource
// SKUs API. Zone-restricted zones (e.g. SkuNotAvailable for the subscription)
// are subtracted from the advertised list, and a SKU that is entirely restricted
// in the location (Location-type restriction) yields no zones, so every returned
// zone is guaranteed to be usable for the SKU. An empty slice means the
// location/SKU combination exposes no usable availability zones. An error is
// returned if the SKU is not present in the location's Resource SKUs response.
func (tc *perItOrDescribeTestContext) AvailableZones(ctx context.Context, vmSize string) ([]string, error) {
	location := tc.Location()
	skus, err := tc.listVirtualMachineResourceSKUs(ctx, location)
	if err != nil {
		return nil, err
	}
	for _, sku := range skus {
		if sku.Name == nil || *sku.Name != vmSize {
			continue
		}
		if skuRestrictedInLocation(sku, location) {
			return nil, nil
		}
		_, available := zonesInLocation(sku, location)
		return available, nil
	}
	return nil, fmt.Errorf("VM size %q not found in Resource SKUs for location %q", vmSize, location)
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
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Failed to get subscription ID: %v", err))
	}
	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Failed to get Azure credentials: %v", err))
	}
	operationsClient := factory.NewDeploymentOperationsClient()
	getOperationsClient := pipeline.NewCachedOperationsClientGetter(
		subscriptionID,
		operationsClient,
		creds,
		tc.perBinaryInvocationTestContext.getClientFactoryOptions(),
	)
	for _, info := range tc.knownDeployments {
		resourceGroupName, deploymentName := info.resourceGroupName, info.deploymentName
		ginkgo.GinkgoLogr.Info("Dumping deployment operations.", "deployment", deploymentName, "resourceGroup", resourceGroupName)
		operations, err := fetchOperationsFor(ctx, getOperationsClient, subscriptionID, resourceGroupName, deploymentName)
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

	var outputData []byte
	var fileExtension string

	if tc.perBinaryInvocationTestContext.compressTimingMetadata {
		// Gzip the encoded data
		var gzipBuffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&gzipBuffer)
		if _, err := gzipWriter.Write(encoded); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to gzip timing metadata")
			return
		}
		if err := gzipWriter.Close(); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to close gzip writer")
			return
		}
		outputData = gzipBuffer.Bytes()
		fileExtension = "yaml.gz"
	} else {
		outputData = encoded
		fileExtension = "yaml"
	}

	for _, dir := range []string{tc.perBinaryInvocationTestContext.sharedDir, filepath.Join(tc.perBinaryInvocationTestContext.artifactDir, "test-timing")} {
		output := filepath.Join(dir, fmt.Sprintf("timing-metadata-%s.%s", hex.EncodeToString(hashBytes), fileExtension))
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to create directory for timing metadata")
			continue
		}
		if err := os.WriteFile(output, outputData, 0644); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Failed to write timing metadata")
			continue
		}

		ginkgo.GinkgoLogr.Info("Wrote timing metadata", "path", output)
	}
}
