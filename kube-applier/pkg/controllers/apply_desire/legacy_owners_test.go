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

package apply_desire

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// TestLegacyOwnerBlockTreatsAbsentOCMAPIsAsNoOwners covers the post-ACM-removal
// management cluster: mgmt-agent deletes every *.open-cluster-management.io CRD,
// so the API server answers these lists with 404. A missing type cannot hold any
// object, so the check must report "no legacy owners" instead of blocking
// finalization forever.
func TestLegacyOwnerBlockTreatsAbsentOCMAPIsAsNoOwners(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), finalizeListKinds())
	for _, gvr := range []schema.GroupVersionResource{manifestWorksGVR, managedClusterAddonsGVR} {
		gvr := gvr
		dyn.PrependReactor("list", gvr.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: gvr.Group, Resource: gvr.Resource}, "")
		})
	}

	blocker, err := (&ApplyDesireController{dyn: dyn}).legacyOwnerBlock(
		context.Background(), finalizeTestHCNS, finalizeTestHCNS+"-"+finalizeTestHCName)
	require.NoError(t, err, "an API type that is not installed is not a read failure")
	require.Empty(t, blocker)
}

// TestLegacyOwnerBlockStillFailsOnReadErrors pins the other half of the rule: a
// lookup that failed for any reason other than the type being absent is not
// evidence that no legacy owner exists, so it must still block.
func TestLegacyOwnerBlockStillFailsOnReadErrors(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), finalizeListKinds())
	dyn.PrependReactor("list", manifestWorksGVR.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("temporary API timeout")
	})

	_, err := (&ApplyDesireController{dyn: dyn}).legacyOwnerBlock(
		context.Background(), finalizeTestHCNS, finalizeTestHCNS+"-"+finalizeTestHCName)
	require.ErrorContains(t, err, "list ManifestWorks")
}
