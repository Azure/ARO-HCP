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

	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
)

func TestMiddlewareLockSubscription(t *testing.T) {
	panicingHandler := func(writer http.ResponseWriter, request *http.Request) {
		panic("force failure")
	}

	stopWasCalled := false
	stopCalledFn := func() *azcosmos.ItemResponse {
		stopWasCalled = true
		return nil
	}
	lockCancelCtx := context.Background()

	ctx := context.Background()
	mockController := gomock.NewController(t)
	mockDBClient := database.NewMockDBClient(mockController)
	mockLockClient := database.NewMockLockClientInterface(mockController)
	mockLockClient.EXPECT().GetDefaultTimeToLive().Return(10 * time.Second)
	lockResponse := &azcosmos.ItemResponse{}
	mockLockClient.EXPECT().AcquireLock(gomock.Not(gomock.Nil()), "TheSubscriptionID", ptr.To(10*time.Second)).Return(lockResponse, nil)
	mockLockClient.EXPECT().HoldLock(gomock.Not(gomock.Nil()), gomock.Not(gomock.Nil())).Return(lockCancelCtx, stopCalledFn)
	mockDBClient.EXPECT().GetLockClient().Return(mockLockClient)

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
