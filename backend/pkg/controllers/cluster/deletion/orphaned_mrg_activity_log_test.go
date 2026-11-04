// Copyright 2026 Microsoft Corporation
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

package deletion

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type activityLogsClientStub struct {
	filter  string
	options *armmonitor.ActivityLogsClientListOptions
	pages   []armmonitor.ActivityLogsClientListResponse
	err     error
}

func (s *activityLogsClientStub) NewListPager(filter string, options *armmonitor.ActivityLogsClientListOptions) *runtime.Pager[armmonitor.ActivityLogsClientListResponse] {
	s.filter = filter
	s.options = options
	index := 0
	return runtime.NewPager(runtime.PagingHandler[armmonitor.ActivityLogsClientListResponse]{
		More: func(page armmonitor.ActivityLogsClientListResponse) bool {
			return page.NextLink != nil
		},
		Fetcher: func(context.Context, *armmonitor.ActivityLogsClientListResponse) (armmonitor.ActivityLogsClientListResponse, error) {
			if s.err != nil {
				return armmonitor.ActivityLogsClientListResponse{}, s.err
			}
			page := s.pages[index]
			index++
			return page, nil
		},
	})
}

func TestLatestFailedResourceGroupDeleteReturnsActivityLogError(t *testing.T) {
	queryErr := errors.New("access denied")
	stub := &activityLogsClientStub{err: queryErr}
	_, err := latestFailedResourceGroupDelete(t.Context(), stub, "/subscriptions/sub1/resourceGroups/managed-rg-1", time.Now())
	require.ErrorIs(t, err, queryErr)
}

func TestLatestFailedResourceGroupDelete(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	failed := func(at time.Time, operation, status string) *armmonitor.EventData {
		return &armmonitor.EventData{
			EventTimestamp: ptr.To(at),
			OperationName:  &armmonitor.LocalizableString{Value: ptr.To(operation)},
			Status:         &armmonitor.LocalizableString{Value: ptr.To(status)},
		}
	}
	latest := failed(now.Add(-10*time.Minute), resourceGroupDelete, "Failed")
	stub := &activityLogsClientStub{pages: []armmonitor.ActivityLogsClientListResponse{
		{EventDataCollection: armmonitor.EventDataCollection{
			Value:    []*armmonitor.EventData{failed(now.Add(-20*time.Minute), resourceGroupDelete, "Failed"), failed(now.Add(-5*time.Minute), resourceGroupDelete, "Succeeded")},
			NextLink: ptr.To("next"),
		}},
		{EventDataCollection: armmonitor.EventDataCollection{
			Value: []*armmonitor.EventData{failed(now.Add(-2*time.Minute), "Microsoft.Compute/virtualMachines/delete", "Failed"), latest},
		}},
	}}
	resourceID := "/subscriptions/sub1/resourceGroups/managed-rg-1"
	event, err := latestFailedResourceGroupDelete(t.Context(), stub, resourceID, now)
	require.NoError(t, err)
	require.Same(t, latest, event)
	require.Equal(t, fmt.Sprintf("eventTimestamp ge '2026-10-20T12:00:00Z' and eventTimestamp le '2026-10-21T12:00:00Z' and resourceUri eq '%s'", resourceID), stub.filter)
	require.Contains(t, *stub.options.Select, "properties")
}
