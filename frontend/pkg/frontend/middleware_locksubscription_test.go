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

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// trackingLockClient is a test double that tracks whether stop was called.
type trackingLockClient struct {
	stopWasCalled *bool
	defaultTTL    time.Duration
}

func (c *trackingLockClient) GetDefaultTimeToLive() time.Duration {
	return c.defaultTTL
}

func (c *trackingLockClient) SetRetryAfterHeader(header http.Header) {
	header.Set("Retry-After", fmt.Sprintf("%d", int(c.defaultTTL.Seconds())))
}

func (c *trackingLockClient) AcquireLock(ctx context.Context, id string, timeout *time.Duration) (*azcosmos.ItemResponse, error) {
	return &azcosmos.ItemResponse{}, nil
}

func (c *trackingLockClient) TryAcquireLock(ctx context.Context, id string) (*azcosmos.ItemResponse, error) {
	return &azcosmos.ItemResponse{}, nil
}

func (c *trackingLockClient) HoldLock(ctx context.Context, item *azcosmos.ItemResponse) (context.Context, database.StopHoldLock) {
	return ctx, func() *azcosmos.ItemResponse {
		*c.stopWasCalled = true
		return nil
	}
}

func (c *trackingLockClient) RenewLock(ctx context.Context, item *azcosmos.ItemResponse) (*azcosmos.ItemResponse, error) {
	return item, nil
}

func (c *trackingLockClient) ReleaseLock(ctx context.Context, item *azcosmos.ItemResponse) error {
	return nil
}

var _ database.LockClientInterface = &trackingLockClient{}

func TestMiddlewareLockSubscription(t *testing.T) {
	panicingHandler := func(writer http.ResponseWriter, request *http.Request) {
		panic("force failure")
	}

	stopWasCalled := false
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	mockDBClient.SetLockClient(&trackingLockClient{
		stopWasCalled: &stopWasCalled,
		defaultTTL:    10 * time.Second,
	})

	request := httptest.NewRequestWithContext(ctx, "PUT", "http://example.com", nil)
	request.SetPathValue(PathSegmentSubscriptionID, "TheSubscriptionID")
	response := httptest.NewRecorder()

	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("Recovery as expected", r)
			}
		}()

		newMiddlewareLockSubscription(mockDBClient).handleRequest(response, request, panicingHandler)
	}()

	if !stopWasCalled {
		t.Error("stop was not called")
	}
}
