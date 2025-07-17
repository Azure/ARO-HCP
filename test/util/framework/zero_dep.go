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
	"hash/fnv"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"k8s.io/apimachinery/pkg/util/rand"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type TestContext struct {
	subscriptionName string
	tenantID         string
	testUserClientID string

	contextLock                   sync.RWMutex
	subscriptionID                string
	azureCredentials              azcore.TokenCredential
	clientFactory20240610         *hcpapi20240610.ClientFactory
	armResourcesClientFactory     *armresources.ClientFactory
	armSubscriptionsClientFactory *armsubscriptions.ClientFactory
}

type CleanupFunc func(ctx context.Context) error

var (
	invocationContext *TestContext
	initializeOnce    sync.Once
)

// InvocationContext requires the following env vars
// CUSTOMER_SUBSCRIPTION
// AZURE_TENANT_ID
// AZURE_CLIENT_ID
// AZURE_CLIENT_SECRET
func InvocationContext() *TestContext {
	initializeOnce.Do(func() {
		invocationContext = &TestContext{
			subscriptionName: subscriptionName(),
			tenantID:         tenantID(),
			testUserClientID: testUserClientID(),
		}
	})
	return invocationContext
}

func (tc *TestContext) NewResourceGroup(ctx context.Context, resourceGroupPrefix, location string) (*armresources.ResourceGroup, CleanupFunc, error) {
	suffix := rand.String(6)
	resourceGroupName := SuffixName(resourceGroupPrefix, suffix, 64)

	resourceGroup, err := CreateResourceGroup(ctx, tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient(), resourceGroupName, location, 10*time.Minute)
	if err != nil {
		return nil, func(ctx context.Context) error { return nil }, fmt.Errorf("failed to create resource group: %w", err)
	}

	return resourceGroup, tc.RecordResourceGroupToCleanup(resourceGroupName), nil
}

// RecordResourceGroupToCleanup has the test context track resource groups
func (tc *TestContext) RecordResourceGroupToCleanup(resourceGroupName string) CleanupFunc {
	return func(ctx context.Context) error {
		return tc.cleanupResourceGroup(ctx, resourceGroupName)
	}
}

// cleanupResourceGroup is the standard resourcegroup cleanup.  It attempts to
// 1. delete all HCP clusters and wait for success
// 2. delete the resource group and wait for success
func (tc *TestContext) cleanupResourceGroup(ctx context.Context, resourceGroupName string) error {
	errs := []error{}

	if hcpClientFactory, err := tc.get20240610ClientFactoryUnlocked(ctx); err == nil {
		err := DeleteAllHCPClusters(ctx, hcpClientFactory.NewHcpOpenShiftClustersClient(), resourceGroupName, 1*time.Second, 60*time.Minute)
		if err != nil {
			return fmt.Errorf("failed to cleanup resource group: %w", err)
		}
	} else {
		errs = append(errs, fmt.Errorf("failed creating client factory for cleanup: %w", err))
	}

	if armClientFactory, err := tc.GetARMResourcesClientFactory(ctx); err == nil {
		err := DeleteResourceGroup(ctx, armClientFactory.NewResourceGroupsClient(), resourceGroupName, 1*time.Second, 60*time.Minute)
		if err != nil {
			return fmt.Errorf("failed to cleanup resource group: %w", err)
		}
	} else {
		errs = append(errs, fmt.Errorf("failed creating client factory for cleanup: %w", err))
	}

	return errors.Join(errs...)
}

func (tc *TestContext) GetARMResourcesClientFactoryOrDie(ctx context.Context) *armresources.ClientFactory {
	return Must(tc.GetARMResourcesClientFactory(ctx))
}

func (tc *TestContext) Get20240610ClientFactoryOrDie(ctx context.Context) *hcpapi20240610.ClientFactory {
	return Must(tc.Get20240610ClientFactory(ctx))
}

func (tc *TestContext) getAzureCredentialsUnlocked() (azcore.TokenCredential, error) {
	if tc.azureCredentials != nil {
		return tc.azureCredentials, nil
	}

	// if we find a desire to use the zero-dep e2e testing everywhere, we can extend this credential creation to include
	// other options for non-Azure endpoints.  It's worth remembering that the value add
	azureCredentials, err := azidentity.NewEnvironmentCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed building environment credential: %w", err)
	}
	tc.azureCredentials = azureCredentials

	return tc.azureCredentials, nil
}

func (tc *TestContext) GetARMSubscriptionsClientFactory() (*armsubscriptions.ClientFactory, error) {
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

func (tc *TestContext) getARMSubscriptionsClientFactoryUnlocked() (*armsubscriptions.ClientFactory, error) {
	if tc.armResourcesClientFactory != nil {
		return tc.armSubscriptionsClientFactory, nil
	}

	creds, err := tc.getAzureCredentialsUnlocked()
	if err != nil {
		return nil, err
	}
	clientFactory, err := armsubscriptions.NewClientFactory(creds, nil)
	if err != nil {
		return nil, err
	}
	tc.armSubscriptionsClientFactory = clientFactory

	return tc.armSubscriptionsClientFactory, nil
}

func (tc *TestContext) GetARMResourcesClientFactory(ctx context.Context) (*armresources.ClientFactory, error) {
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

func (tc *TestContext) getARMResourcesClientFactoryUnlocked(ctx context.Context) (*armresources.ClientFactory, error) {
	if tc.armResourcesClientFactory != nil {
		return tc.armResourcesClientFactory, nil
	}

	creds, err := tc.getAzureCredentialsUnlocked()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := armresources.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return nil, err
	}
	tc.armResourcesClientFactory = clientFactory

	return tc.armResourcesClientFactory, nil
}

func (tc *TestContext) Get20240610ClientFactory(ctx context.Context) (*hcpapi20240610.ClientFactory, error) {
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

func (tc *TestContext) get20240610ClientFactoryUnlocked(ctx context.Context) (*hcpapi20240610.ClientFactory, error) {
	if tc.clientFactory20240610 != nil {
		return tc.clientFactory20240610, nil
	}

	creds, err := tc.getAzureCredentialsUnlocked()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpapi20240610.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return nil, err
	}
	tc.clientFactory20240610 = clientFactory

	return tc.clientFactory20240610, nil
}

func (tc *TestContext) getSubscriptionIDUnlocked(ctx context.Context) (string, error) {
	if len(tc.subscriptionID) > 0 {
		return tc.subscriptionID, nil
	}

	clientFactory, err := tc.getARMSubscriptionsClientFactoryUnlocked()
	if err != nil {
		return "", fmt.Errorf("failed to get ARM subscriptions client factory: %w", err)
	}

	subscriptionID, err := GetSubscriptionID(ctx, clientFactory.NewClient(), tc.subscriptionName)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}
	tc.subscriptionID = subscriptionID

	return tc.subscriptionID, nil
}

// subscriptionName returns the value of CUSTOMER_SUBSCRIPTION environment variable
func subscriptionName() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("CUSTOMER_SUBSCRIPTION")
}

// testUserClientID returns the value of AZURE_CLIENT_ID environment variable
func testUserClientID() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("AZURE_CLIENT_ID")
}

// tenantID returns the value of AZURE_TENANT_ID environment variable
func tenantID() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("AZURE_TENANT_ID")
}

// Must is a generic function that takes a value of type T and an error.
// If the error is not nil, it panics with the error.
// Otherwise, it returns the value of type T.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// SuffixName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than a valid pod name, it will truncate the base name and add an 8-character hash
// of the [base]-[suffix] string.
func SuffixName(base, suffix string, maxLen int) string {
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) > maxLen {
		prefix := base[0:min(len(base), maxLen-9)]
		// Calculate hash on initial base-suffix string
		name = fmt.Sprintf("%s-%s", prefix, hash(name))
	}
	return name
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}
