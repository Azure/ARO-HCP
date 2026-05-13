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

package kubeapplier

import (
	"bytes"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

// fixtureApplyDesire builds a populated ApplyDesire whose JSON form exercises
// every nontrivial field, so deep-copy and round-trip tests can compare bytes.
func fixtureApplyDesire(t *testing.T) *ApplyDesire {
	t.Helper()
	id, err := azcorearm.ParseResourceID(ToClusterScopedApplyDesireResourceIDString(
		"00000000-0000-0000-0000-000000000001", "myRG", "myCluster", "myDesire",
	))
	if err != nil {
		t.Fatalf("parse resource id: %v", err)
	}
	return &ApplyDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{ResourceID: id},
		Spec: ApplyDesireSpec{
			ManagementCluster: "mgmt-1",
			KubeContent: &runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x","namespace":"default"},"data":{"key":"value"}}`),
			},
		},
		Status: ApplyDesireStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: ConditionReasonNoErrors, Message: "ok"},
			},
		},
	}
}

func fixtureDeleteDesire(t *testing.T) *DeleteDesire {
	t.Helper()
	id, err := azcorearm.ParseResourceID(ToClusterScopedDeleteDesireResourceIDString(
		"00000000-0000-0000-0000-000000000001", "myRG", "myCluster", "myDesire",
	))
	if err != nil {
		t.Fatalf("parse resource id: %v", err)
	}
	return &DeleteDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{ResourceID: id},
		Spec: DeleteDesireSpec{
			ManagementCluster: "mgmt-1",
			TargetItem: ResourceReference{
				Group: "apps", Resource: "deployments", Namespace: "ns", Name: "x",
			},
		},
		Status: DeleteDesireStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: ConditionReasonWaitingForDeletion, Message: "uid=abc"},
			},
		},
	}
}

func fixtureReadDesire(t *testing.T) *ReadDesire {
	t.Helper()
	id, err := azcorearm.ParseResourceID(ToNodePoolScopedReadDesireResourceIDString(
		"00000000-0000-0000-0000-000000000001", "myRG", "myCluster", "myNodePool", "myDesire",
	))
	if err != nil {
		t.Fatalf("parse resource id: %v", err)
	}
	return &ReadDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{ResourceID: id},
		Spec: ReadDesireSpec{
			ManagementCluster: "mgmt-1",
			TargetItem: ResourceReference{
				Group: "", Resource: "configmaps", Namespace: "default", Name: "x",
			},
		},
		Status: ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: ConditionReasonNoErrors},
			},
			KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)},
		},
	}
}

func TestRuntimeObjectAndJSONRoundTrip(t *testing.T) {
	type tc struct {
		name string
		// obj is built fresh per subtest so DeepCopyObject can be exercised on
		// a stable starting value.
		newObj func(t *testing.T) runtime.Object
	}
	cases := []tc{
		{name: "ApplyDesire", newObj: func(t *testing.T) runtime.Object { return fixtureApplyDesire(t) }},
		{name: "DeleteDesire", newObj: func(t *testing.T) runtime.Object { return fixtureDeleteDesire(t) }},
		{name: "ReadDesire", newObj: func(t *testing.T) runtime.Object { return fixtureReadDesire(t) }},
	}
	for _, c := range cases {
		t.Run(c.name+"/DeepCopyEqualsOriginal", func(t *testing.T) {
			original := c.newObj(t)
			origJSON, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal original: %v", err)
			}
			copy := original.DeepCopyObject()
			copyJSON, err := json.Marshal(copy)
			if err != nil {
				t.Fatalf("marshal copy: %v", err)
			}
			if !bytes.Equal(origJSON, copyJSON) {
				t.Errorf("DeepCopy produced different JSON\n got: %s\nwant: %s", copyJSON, origJSON)
			}
		})
		t.Run(c.name+"/DeepCopyIsIndependent", func(t *testing.T) {
			original := c.newObj(t)
			origJSON, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal original: %v", err)
			}
			copy := original.DeepCopyObject()
			// Mutate the copy through type assertions; verify origJSON is unaffected.
			switch v := copy.(type) {
			case *ApplyDesire:
				v.Status.Conditions[0].Message = "mutated"
				v.Spec.KubeContent.Raw = append([]byte(nil), v.Spec.KubeContent.Raw...)
				v.Spec.KubeContent.Raw[0] = 'X'
			case *DeleteDesire:
				v.Status.Conditions[0].Message = "mutated"
			case *ReadDesire:
				v.Status.Conditions[0].Message = "mutated"
				v.Status.KubeContent.Raw = append([]byte(nil), v.Status.KubeContent.Raw...)
				v.Status.KubeContent.Raw[0] = 'X'
			}
			afterJSON, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("re-marshal original: %v", err)
			}
			if !bytes.Equal(origJSON, afterJSON) {
				t.Errorf("mutating the copy changed the original\nbefore: %s\n after: %s", origJSON, afterJSON)
			}
		})
		t.Run(c.name+"/JSONRoundTrip", func(t *testing.T) {
			original := c.newObj(t)
			origJSON, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Decode into a fresh empty value of the same dynamic type and
			// re-marshal. Bytes must match.
			var roundTripped runtime.Object
			switch original.(type) {
			case *ApplyDesire:
				roundTripped = &ApplyDesire{}
			case *DeleteDesire:
				roundTripped = &DeleteDesire{}
			case *ReadDesire:
				roundTripped = &ReadDesire{}
			}
			if err := json.Unmarshal(origJSON, roundTripped); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			rtJSON, err := json.Marshal(roundTripped)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if !bytes.Equal(origJSON, rtJSON) {
				t.Errorf("JSON round-trip mismatch\norig: %s\n got: %s", origJSON, rtJSON)
			}
		})
	}
}

func TestObjectMetaAccessor(t *testing.T) {
	apply := fixtureApplyDesire(t)
	delete := fixtureDeleteDesire(t)
	read := fixtureReadDesire(t)
	for _, o := range []interface{ GetObjectMeta() metav1.Object }{apply, delete, read} {
		if name := o.GetObjectMeta().GetName(); name == "" {
			t.Errorf("GetObjectMeta returned empty Name; should reflect lowercased ResourceID")
		}
	}
}
