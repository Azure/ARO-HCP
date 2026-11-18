// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package admission

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestNodePoolDeleteTimeoutMutation(t *testing.T) {
	registered := &coreapi.Subscription{Properties: &coreapi.SubscriptionProperties{
		RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(metadataapi.FeatureExperimentalReleaseFeatures), State: ptr.To("Registered")}},
	}}
	for _, op := range []operation.Type{operation.Create, operation.Update} {
		for _, tc := range []struct {
			name string
			tag  string
			sub  *coreapi.Subscription
			want *time.Duration
			err  string
		}{
			{name: "override", tag: "2h", sub: registered, want: ptr.To(2 * time.Hour)},
			{name: "minimum", tag: "1m", sub: registered, want: ptr.To(time.Minute)},
			{name: "remove tag clears old override", sub: registered},
			{name: "unregistered ignores invalid override", tag: "bad", sub: &coreapi.Subscription{}},
			{name: "nil subscription clears override", tag: "2h"},
			{name: "invalid", tag: "bad", sub: registered, err: "valid Go duration"},
			{name: "too short", tag: "59s", sub: registered, err: "at least 1m0s"},
		} {
			t.Run(fmt.Sprint(op)+"/"+tc.name, func(t *testing.T) {
				old := &coreapi.NodePool{ServiceProviderProperties: coreapi.NodePoolServiceProviderProperties{DeleteOperationCompletionTimeout: ptr.To(time.Hour)}}
				obj := old.DeepCopy()
				obj.Tags = map[string]string{strings.ToUpper(metadataapi.TagNodePoolMaxDeletionDuration): tc.tag}
				ctx := &NodePoolAdmissionContext{Clock: clocktesting.NewFakePassiveClock(time.Unix(100, 0)), Subscription: tc.sub, Cluster: &coreapi.Cluster{}, OriginalNodePool: obj.DeepCopy()}
				errs := MutateNodePool(t.Context(), ctx, operation.Operation{Type: op}, obj, old)
				require.Equal(t, time.Hour, *old.ServiceProviderProperties.DeleteOperationCompletionTimeout, "stored object must not be mutated")
				if tc.err != "" {
					require.Len(t, errs, 1)
					require.Contains(t, errs[0].Detail, tc.err)
					return
				}
				require.Empty(t, errs)
				require.Equal(t, tc.want, obj.ServiceProviderProperties.DeleteOperationCompletionTimeout)
			})
		}
	}
}
