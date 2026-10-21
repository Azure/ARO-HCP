// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type staticToken string

func (s staticToken) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: string(s), ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// perSubscription hands out one token per subscription, like several service
// principals logged into one Azure CLI profile.
type perSubscription map[string]staticToken

func (p perSubscription) GetToken(ctx context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return staticToken("DEFAULT").GetToken(ctx, o)
}

func (p perSubscription) ForSubscription(sub string) azcore.TokenCredential { return p[sub] }

func TestCollectUsesEachSubscriptionsIdentity(t *testing.T) {
	const otherSub = "11111111-1111-1111-1111-111111111111"
	s := billingSnapshot()
	s.Groups = append(s.Groups, Group{SubscriptionID: otherSub, Name: "other", Owner: "test", BillingStatus: "pending"})
	seen := map[string]string{}
	client := &http.Client{Transport: billingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "management.azure.com" {
			return billingResponse(200, billingTestHeader), nil
		}
		sub := strings.Split(r.URL.Path, "/")[2]
		seen[sub] = r.Header.Get("Authorization")
		return billingResponse(200, billingManifest(billingTestBlob)), nil
	})}
	Collect(context.Background(), client, perSubscription{billingTestSub: "TOKEN-A", otherSub: "TOKEN-B"}, s)
	if seen[billingTestSub] != "Bearer TOKEN-A" || seen[otherSub] != "Bearer TOKEN-B" {
		t.Fatalf("billing did not use each subscription's identity: %v", seen)
	}
}
