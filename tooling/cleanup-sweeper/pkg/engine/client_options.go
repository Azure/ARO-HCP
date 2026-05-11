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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/azsdk"
)

var defaultARMRetryOptions = policy.RetryOptions{
	// The Azure SDK defaults MaxRetryDelay to 60 seconds and skips retries when
	// Retry-After exceeds that cap. Cleanup workflows regularly see longer
	// server-provided delays when subscriptions are throttled.
	MaxRetryDelay: 5 * time.Minute,
}

// DefaultARMClientOptions returns cleanup-sweeper's default ARM client options.
func DefaultARMClientOptions() *azcorearm.ClientOptions {
	clientOpts := azsdk.NewClientOptions(azsdk.ComponentResourceCleaner)
	clientOpts.Retry = defaultARMRetryOptions
	return &azcorearm.ClientOptions{
		ClientOptions: clientOpts,
	}
}

func normalizeARMClientOptions(opts *azcorearm.ClientOptions) *azcorearm.ClientOptions {
	if opts != nil {
		return opts
	}
	return DefaultARMClientOptions()
}
