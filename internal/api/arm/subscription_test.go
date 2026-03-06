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

package arm

import (
	"testing"
)

func TestHasRegisteredFeature(t *testing.T) {
	registeredState := "Registered"
	notRegisteredState := "NotRegistered"
	featureName := "Microsoft.RedHatOpenShift/PlatformSubscription"
	featureNameLower := "microsoft.Redhatopenshift/platformsubscription"

	tests := []struct {
		name        string
		sub         *Subscription
		featureName string
		want        bool
	}{
		{
			name:        "nil properties",
			sub:         &Subscription{Properties: nil},
			featureName: featureName,
			want:        false,
		},
		{
			name:        "nil registered features",
			sub:         &Subscription{Properties: &SubscriptionProperties{RegisteredFeatures: nil}},
			featureName: featureName,
			want:        false,
		},
		{
			name: "empty registered features",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{},
			}},
			featureName: featureName,
			want:        false,
		},
		{
			name: "feature not found",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: ptr("Microsoft.Other/Feature"), State: &registeredState},
				},
			}},
			featureName: featureName,
			want:        false,
		},
		{
			name: "feature found but not registered",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: &featureName, State: &notRegisteredState},
				},
			}},
			featureName: featureName,
			want:        false,
		},
		{
			name: "feature found and registered - exact match",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: &featureName, State: &registeredState},
				},
			}},
			featureName: featureName,
			want:        true,
		},
		{
			name: "feature found and registered - case insensitive lookup",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: &featureName, State: &registeredState},
				},
			}},
			featureName: featureNameLower,
			want:        true,
		},
		{
			name: "feature found and registered - case insensitive stored",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: &featureNameLower, State: &registeredState},
				},
			}},
			featureName: featureName,
			want:        true,
		},
		{
			name: "feature found and registered - mixed case",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: ptr("MICROSOFT.REDHATOPENSHIFT/PLATFORMSUBSCRIPTION"), State: &registeredState},
				},
			}},
			featureName: featureName,
			want:        true,
		},
		{
			name: "feature with nil name",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: nil, State: &registeredState},
				},
			}},
			featureName: featureName,
			want:        false,
		},
		{
			name: "feature with nil state",
			sub: &Subscription{Properties: &SubscriptionProperties{
				RegisteredFeatures: &[]Feature{
					{Name: &featureName, State: nil},
				},
			}},
			featureName: featureName,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sub.HasRegisteredFeature(tt.featureName)
			if got != tt.want {
				t.Errorf("HasRegisteredFeature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}
