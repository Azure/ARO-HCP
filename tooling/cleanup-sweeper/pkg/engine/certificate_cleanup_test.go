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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
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

// failingCredential fails every token request, so any Azure call made with it
// fails before reaching the network.
type failingCredential struct{}

func (failingCredential) GetToken(context.Context, azpolicy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("no credentials in tests")
}

func TestCertificateCleanupWorkflowResolvesOwnerSubscriptionsOnlyForActiveDeletion(t *testing.T) {
	certificatePolicy := policy.CertificatePolicy{
		VaultURL:       "https://example.vault.azure.net",
		OwnerInventory: policy.CertificateOwnerInventory{Subscriptions: []string{"Example Subscription"}},
		Discovery: policy.CertificateDiscoveryPolicy{
			ProtectedTags: map[string]string{"persist": "true"},
			Rules: []policy.CertificateDiscoveryRule{{
				Name:       "guarded",
				Action:     policy.CertificateDiscoveryActionDelete,
				Match:      policy.CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x-(?P<owner>[a-z0-9]+)$`), OwnerCaptureGroup: "owner"},
				Conditions: policy.CertificateDiscoveryConditions{OwnerResourceGroupAbsent: true},
				OlderThan:  24 * time.Hour,
			}},
		},
	}
	options := func(deleteActive bool) CertificateWorkflowOptions {
		return CertificateWorkflowOptions{
			DryRun: true, DeleteActive: deleteActive, PurgeDeleted: true,
			MaxDeletions: 1, MaxPurges: 1, Parallelism: 1, Policy: certificatePolicy,
		}
	}

	// A purge-only run must not need visibility of the owner subscriptions, even
	// though the policy contains a guarded delete rule.
	if _, err := CertificateCleanupWorkflow(t.Context(), failingCredential{}, options(false)); err != nil {
		t.Fatalf("purge-only workflow must not resolve owner subscriptions: %v", err)
	}
	// Deleting active certificates does need them, and fails closed without them.
	if _, err := CertificateCleanupWorkflow(t.Context(), failingCredential{}, options(true)); err == nil || !strings.Contains(err.Error(), "failed to list subscriptions") {
		t.Fatalf("error = %v, want subscription listing failure", err)
	}
}
