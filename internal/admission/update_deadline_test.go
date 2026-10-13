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

package admission

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestUpdateOperationCompletionDeadline(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	clock := clocktesting.NewFakePassiveClock(now)
	oldDeadline := metav1.NewTime(now.Add(-time.Hour))
	subscription := &coreapi.Subscription{Properties: &coreapi.SubscriptionProperties{
		RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(metadataapi.FeatureExperimentalReleaseFeatures), State: ptr.To("Registered")}},
	}}
	for _, resource := range []struct {
		name   string
		tag    string
		mutate func(*testing.T, *coreapi.Subscription, map[string]string, operation.Type) (*metav1.Time, field.ErrorList)
	}{
		{
			name: "cluster", tag: metadataapi.TagClusterMaxUpdateDuration,
			mutate: func(t *testing.T, sub *coreapi.Subscription, tags map[string]string, op operation.Type) (*metav1.Time, field.ErrorList) {
				old := &coreapi.Cluster{}
				old.ServiceProviderProperties.CreateOperationCompletionDeadline = &oldDeadline
				old.ServiceProviderProperties.UpdateOperationCompletionDeadline = &oldDeadline
				obj := old.DeepCopy()
				obj.Tags = tags
				if op == operation.Create {
					obj.ServiceProviderProperties.UpdateOperationCompletionDeadline = nil
				}
				ctx := &ClusterAdmissionContext{Clock: clock, Subscription: sub, OriginalCluster: obj.DeepCopy()}
				errs := MutateCluster(t.Context(), ctx, operation.Operation{Type: op}, obj, old)
				if op == operation.Update {
					require.Equal(t, oldDeadline, *obj.ServiceProviderProperties.CreateOperationCompletionDeadline, "updates must preserve the creation deadline")
				}
				require.Equal(t, oldDeadline, *old.ServiceProviderProperties.UpdateOperationCompletionDeadline, "mutation must not modify the stored resource")
				return obj.ServiceProviderProperties.UpdateOperationCompletionDeadline, errs
			},
		},
		{
			name: "node pool", tag: metadataapi.TagNodePoolMaxUpdateDuration,
			mutate: func(t *testing.T, sub *coreapi.Subscription, tags map[string]string, op operation.Type) (*metav1.Time, field.ErrorList) {
				old := &coreapi.NodePool{}
				old.ServiceProviderProperties.CreateOperationCompletionDeadline = &oldDeadline
				old.ServiceProviderProperties.UpdateOperationCompletionDeadline = &oldDeadline
				obj := old.DeepCopy()
				obj.Tags = tags
				if op == operation.Create {
					obj.ServiceProviderProperties.UpdateOperationCompletionDeadline = nil
				}
				ctx := &NodePoolAdmissionContext{Clock: clock, Subscription: sub, OriginalNodePool: obj.DeepCopy(), Cluster: &coreapi.Cluster{}}
				errs := MutateNodePool(t.Context(), ctx, operation.Operation{Type: op}, obj, old)
				if op == operation.Update {
					require.Equal(t, oldDeadline, *obj.ServiceProviderProperties.CreateOperationCompletionDeadline, "updates must preserve the creation deadline")
				}
				require.Equal(t, oldDeadline, *old.ServiceProviderProperties.UpdateOperationCompletionDeadline, "mutation must not modify the stored resource")
				return obj.ServiceProviderProperties.UpdateOperationCompletionDeadline, errs
			},
		},
	} {
		t.Run(resource.name, func(t *testing.T) {
			for _, tt := range []struct {
				name         string
				subscription *coreapi.Subscription
				duration     string
				uppercase    bool
				create       bool
				wantDuration time.Duration
				wantError    string
			}{
				{name: "default resets previous deadline", wantDuration: time.Hour},
				{name: "registered without tag", subscription: subscription, wantDuration: time.Hour},
				{name: "empty tag", subscription: subscription, duration: "", wantDuration: time.Hour},
				{name: "override", subscription: subscription, duration: "1h30m", wantDuration: 90 * time.Minute},
				{name: "case insensitive", subscription: subscription, duration: "25m", uppercase: true, wantDuration: 25 * time.Minute},
				{name: "minimum", subscription: subscription, duration: "1m", wantDuration: time.Minute},
				{name: "nil subscription ignores override", duration: "25m", wantDuration: time.Hour},
				{name: "unregistered ignores invalid tag", subscription: &coreapi.Subscription{}, duration: "invalid", wantDuration: time.Hour},
				{name: "invalid", subscription: subscription, duration: "invalid", wantError: "must be a valid Go duration"},
				{name: "too short", subscription: subscription, duration: "30s", wantError: "must be at least 1m0s"},
				{name: "zero", subscription: subscription, duration: "0s", wantError: "must be at least 1m0s"},
				{name: "negative", subscription: subscription, duration: "-1m", wantError: "must be at least 1m0s"},
				{name: "create does not set update deadline", subscription: subscription, duration: "25m", create: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					key := resource.tag
					if tt.uppercase {
						key = strings.ToUpper(key)
					}
					tags := map[string]string{key: tt.duration}
					if tt.name == "registered without tag" {
						tags = nil
					}
					op := operation.Update
					if tt.create {
						op = operation.Create
					}
					deadline, errs := resource.mutate(t, tt.subscription, tags, op)
					if tt.wantError != "" {
						require.Len(t, errs, 1)
						require.Equal(t, field.NewPath("tags").Key(resource.tag).String(), errs[0].Field)
						require.Contains(t, errs[0].Detail, tt.wantError)
						return
					}
					require.Empty(t, errs)
					if tt.create {
						require.Nil(t, deadline)
						return
					}
					require.NotNil(t, deadline)
					require.Equal(t, now.Add(tt.wantDuration), deadline.Time)
				})
			}
		})
	}
}
