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
	"fmt"
	"os"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type TestContext struct {
	subscriptionID   string
	tenantID         string
	testUserClientID string

	contextLock           sync.RWMutex
	clientFactory20240610 *hcpapi20240610.ClientFactory
}

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
			subscriptionID:   subscriptionID(),
			tenantID:         tenantID(),
			testUserClientID: testUserClientID(),
		}
	})
	return invocationContext
}

func (tc *TestContext) Get20240610ClientFactoryOrDie() *hcpapi20240610.ClientFactory {
	clientFactory, err := tc.Get20240610ClientFactory()
	if err != nil {
		panic(err)
	}
	return clientFactory
}

func (tc *TestContext) Get20240610ClientFactory() (*hcpapi20240610.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20240610 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20240610, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	// if we find a desire to use the zero-dep e2e testing everywhere, we can extend this credential creation to include
	// other options for non-Azure endpoints.  It's worth remembering that the value add
	creds, err := azidentity.NewEnvironmentCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed building environment credential: %w", err)
	}
	clientFactory, err := hcpapi20240610.NewClientFactory(tc.subscriptionID, creds, nil)
	if err != nil {
		return nil, err
	}
	tc.clientFactory20240610 = clientFactory

	return tc.clientFactory20240610, nil
}

// subscriptionID returns the value of CUSTOMER_SUBSCRIPTION environment variable
func subscriptionID() string {
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
