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

package cosmosratelimit

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type bucketContextKey struct{}

func contextWithBucket(ctx context.Context, bucket *TokenBucket) context.Context {
	buckets, _ := ctx.Value(bucketContextKey{}).([]*TokenBucket)
	for _, existing := range buckets {
		if existing == bucket {
			return ctx
		}
	}
	// Do not mutate slices inherited from contexts shared by concurrent calls.
	copied := make([]*TokenBucket, len(buckets)+1)
	copy(copied, buckets)
	copied[len(buckets)] = bucket
	return context.WithValue(ctx, bucketContextKey{}, copied)
}

// NewPolicy enforces the buckets selected by CRUD layers, then charges each
// bucket from x-ms-request-charge on every response, including failed attempts.
// Install it once in ClientOptions.PerRetryPolicies so retries and query pages
// each wait and pay their own cost. Requests outside a CRUD layer pass through.
func NewPolicy() policy.Policy { return &requestChargePolicy{} }

type requestChargePolicy struct{}

func (*requestChargePolicy) Do(req *policy.Request) (*http.Response, error) {
	ctx := req.Raw().Context()
	buckets, _ := ctx.Value(bucketContextKey{}).([]*TokenBucket)
	for _, bucket := range buckets {
		if err := bucket.Wait(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := req.Next()
	if resp != nil {
		if charge, parseErr := strconv.ParseFloat(resp.Header.Get("x-ms-request-charge"), 64); parseErr == nil {
			for _, bucket := range buckets {
				bucket.Consume(charge)
			}
		}
	}
	return resp, err
}
