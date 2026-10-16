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

package coreapihelpers

import (
	"iter"
	"path"
	"slices"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func ToSubscriptionResourceID(subscriptionName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToSubscriptionResourceIDString(subscriptionName))
}

func ToSubscriptionResourceIDString(subscriptionName string) string {
	return strings.ToLower(path.Join("/subscriptions", subscriptionName))
}

// ListSubscriptionStates returns an iterator that yields all recognized
// SubscriptionState values. This function is intended as a test aid.
func ListSubscriptionStates() iter.Seq[coreapi.SubscriptionState] {
	return slices.Values([]coreapi.SubscriptionState{
		coreapi.SubscriptionStateRegistered,
		coreapi.SubscriptionStateUnregistered,
		coreapi.SubscriptionStateWarned,
		coreapi.SubscriptionStateDeleted,
		coreapi.SubscriptionStateSuspended,
	})
}
