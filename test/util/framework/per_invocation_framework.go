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
	"hash/fnv"
	"os"
	"path"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/onsi/ginkgo/v2/types"
)

type perBinaryInvocationTestContext struct {
	artifactDir      string
	sharedDir        string
	subscriptionName string
	tenantID         string
	testUserClientID string
	location         string

	contextLock      sync.RWMutex
	subscriptionID   string
	azureCredentials azcore.TokenCredential
}

type CleanupFunc func(ctx context.Context) error

var (
	invocationContextInstance *perBinaryInvocationTestContext
	initializeOnce            sync.Once
)

const (
	StandardPollInterval = 10 * time.Second
)

// InvocationContext requires the following env vars
// CUSTOMER_SUBSCRIPTION
// AZURE_TENANT_ID
// AZURE_CLIENT_ID
// AZURE_CLIENT_SECRET
func invocationContext() *perBinaryInvocationTestContext {
	initializeOnce.Do(func() {
		invocationContextInstance = &perBinaryInvocationTestContext{
			artifactDir:      artifactDir(),
			sharedDir:        sharedDir(),
			subscriptionName: subscriptionName(),
			tenantID:         tenantID(),
			testUserClientID: testUserClientID(),
			location:         location(),
		}
	})
	return invocationContextInstance
}

func NewRootInvocationContext() *perBinaryInvocationTestContext {
	return invocationContext()
}

func (tc *perBinaryInvocationTestContext) TestUserClientIDValue() string {
	return tc.testUserClientID
}

func (tc *perBinaryInvocationTestContext) TenantIDValue() string {
	return tc.tenantID
}

func (tc *perBinaryInvocationTestContext) getAzureCredentials() (azcore.TokenCredential, error) {
	tc.contextLock.RLock()
	if tc.azureCredentials != nil {
		defer tc.contextLock.RUnlock()
		return tc.azureCredentials, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	if tc.azureCredentials != nil {
		return tc.azureCredentials, nil
	}

	azureCredentials, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed building environment credential: %w", err)
	}
	tc.azureCredentials = azureCredentials

	return azureCredentials, nil
}

func (tc *perBinaryInvocationTestContext) getSubscriptionID(ctx context.Context, subscriptionClient *armsubscriptions.Client) (string, error) {
	tc.contextLock.RLock()
	if len(tc.subscriptionID) > 0 {
		defer tc.contextLock.RUnlock()
		return tc.subscriptionID, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	if len(tc.subscriptionID) > 0 {
		return tc.subscriptionID, nil
	}
	subscriptionID, err := GetSubscriptionID(ctx, subscriptionClient, tc.subscriptionName)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}
	tc.subscriptionID = subscriptionID

	return tc.subscriptionID, nil
}

func (tc *perBinaryInvocationTestContext) Location() string {
	return tc.location
}

// artifactDir returns the value of ARTIFACT_DIR environment variable
func artifactDir() string {
	return os.Getenv("ARTIFACT_DIR")
}

// sharedDir is SHARED_DIR.  It is a spot to store *files only* that can be shared between ci-operator steps.
func sharedDir() string {
	return os.Getenv("SHARED_DIR")
}

// We can use this for anything, but currently we have a backup cleanup and collection scripts that use files
// here to cleanup and debug testing resources.
func sharedDir() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("SHARED_DIR")
}

// subscriptionName returns the value of CUSTOMER_SUBSCRIPTION environment variable
func subscriptionName() string {
	return os.Getenv("CUSTOMER_SUBSCRIPTION")
}

func location() string {
	return os.Getenv("LOCATION")
}

func testUserClientID() string {
	return os.Getenv("AZURE_CLIENT_ID")
}

func tenantID() string {
	return os.Getenv("AZURE_TENANT_ID")
}

func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func SuffixName(base, suffix string, maxLen int) string {
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) > maxLen {
		prefix := base[0:min(len(base), maxLen-9)]
		name = fmt.Sprintf("%s-%s", prefix, hash(name))
	}
	return name
}

func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	return fmt.Sprintf("%08x", hash.Sum32())
}

func AnnotatedLocation(annotation string) types.CodeLocation {
	return AnnotatedLocationWithOffset(annotation, 1)
}

func AnnotatedLocationWithOffset(annotation string, offset int) types.CodeLocation {
	codeLocation := types.NewCodeLocation(offset + 1)
	codeLocation.FileName = path.Base(codeLocation.FileName)
	return types.NewCustomCodeLocation(annotation + " | " + codeLocation.String())
}
