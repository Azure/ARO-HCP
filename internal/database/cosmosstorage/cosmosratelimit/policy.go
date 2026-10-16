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

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
)

// NewPolicy enforces the bucket bound to the Cosmos client, then
// charges that bucket from x-ms-request-charge on every response, including
// failed attempts.
// Install it once in ClientOptions.PerRetryPolicies so retries and query pages
// each wait and pay their own cost. All requests from the client share the same bucket, including transactions.
func NewPolicy(bucket *TokenBucket) policy.Policy {
	if bucket == nil {
		panic("cosmos rate limiter policy requires a token bucket")
	}
	return &requestChargePolicy{bucket: bucket, metrics: bucket.metrics}
}

type requestChargePolicy struct {
	bucket  *TokenBucket
	metrics *rateLimitMetrics
}

func (p *requestChargePolicy) Do(req *policy.Request) (*http.Response, error) {
	ctx := context.WithValue(req.Raw().Context(), waitRequestKey{}, req.Raw())
	if err := p.bucket.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := req.Next()
	p.metrics.requests.WithLabelValues(cosmosmetrics.LabelValues(ctx, req.Raw(), resp)...).Inc()
	if resp != nil {
		if charge, parseErr := strconv.ParseFloat(resp.Header.Get("x-ms-request-charge"), 64); parseErr == nil {
			p.bucket.Consume(charge)
		}
	}
	return resp, err
}
