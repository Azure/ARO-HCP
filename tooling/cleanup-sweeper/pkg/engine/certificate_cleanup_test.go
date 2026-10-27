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

package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type fakeSubscriptionLister struct {
	pages     [][]*armsubscriptions.Subscription
	listError int // 1-based page number
}

func (f fakeSubscriptionLister) NewListPager(*armsubscriptions.ClientListOptions) *runtime.Pager[armsubscriptions.ClientListResponse] {
	i := 0
	return runtime.NewPager(runtime.PagingHandler[armsubscriptions.ClientListResponse]{
		More: func(armsubscriptions.ClientListResponse) bool { return i < len(f.pages) },
		Fetcher: func(context.Context, *armsubscriptions.ClientListResponse) (armsubscriptions.ClientListResponse, error) {
			i++
			if i == f.listError {
				return armsubscriptions.ClientListResponse{}, errors.New("subscription page failed")
			}
			return armsubscriptions.ClientListResponse{SubscriptionListResult: armsubscriptions.SubscriptionListResult{Value: f.pages[i-1]}}, nil
		},
	})
}

func subscription(name, id string) *armsubscriptions.Subscription {
	return &armsubscriptions.Subscription{DisplayName: to.Ptr(name), SubscriptionID: to.Ptr(id)}
}

func TestResolveSubscriptionIDs(t *testing.T) {
	lister := fakeSubscriptionLister{pages: [][]*armsubscriptions.Subscription{
		{subscription("Sub A", "id-a"), nil, {DisplayName: to.Ptr("no id")}},
		{subscription("Sub B", "id-b"), subscription("Twin", "id-t1"), subscription("Twin", "id-t2")},
	}}

	got, err := resolveSubscriptionIDs(t.Context(), lister, []string{"Sub B", "Sub A"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"id-b", "id-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v in configured order", got, want)
	}

	for name, test := range map[string]struct {
		lister  fakeSubscriptionLister
		names   []string
		wantErr string
	}{
		"unknown name":         {lister: lister, names: []string{"Sub A", "Sub C"}, wantErr: `"Sub C" was not found`},
		"name match is exact":  {lister: lister, names: []string{"sub a"}, wantErr: "was not found"},
		"ambiguous name":       {lister: lister, names: []string{"Twin"}, wantErr: "ambiguous"},
		"listing fails":        {lister: fakeSubscriptionLister{pages: lister.pages, listError: 2}, names: []string{"Sub A"}, wantErr: "failed to list subscriptions"},
		"subscription w/o  id": {lister: lister, names: []string{"no id"}, wantErr: "was not found"},
	} {
		t.Run(name, func(t *testing.T) {
			if ids, err := resolveSubscriptionIDs(t.Context(), test.lister, test.names); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ids = %v, error = %v, want %q", ids, err, test.wantErr)
			}
		})
	}
}
