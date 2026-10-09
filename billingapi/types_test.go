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

package billingapi_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/billingapi"
)

const testResourceID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"

func TestBillingDocumentJSON(t *testing.T) {
	const fullDocument = `{
		"id":"billing-doc", "subscriptionId":"sub", "creationTime":"2026-10-09T09:00:00Z",
		"deletionTime":"2026-10-09T12:00:00Z", "location":"eastus", "tenantId":"tenant",
		"resourceId":"` + testResourceID + `",
		"managedResourceGroup":"/subscriptions/sub/resourceGroups/managed-rg",
		"lastBillingTimeOfVMEvent":"2026-10-09T10:00:00Z", "lastBillingTimeOfMngtEvent":"2026-10-09T11:00:00Z"
	}`
	for _, tc := range []struct {
		name     string
		input    string
		wantJSON string
		wantErr  bool
	}{
		{name: "complete flat document", input: fullDocument, wantJSON: fullDocument},
		{name: "missing optional fields", input: `{"id":"legacy","creationTime":"2026-10-09T09:00:00Z"}`, wantJSON: `{"id":"legacy","creationTime":"2026-10-09T09:00:00Z"}`},
		{name: "null optional fields", input: `{"creationTime":"2026-10-09T09:00:00Z","resourceId":null,"deletionTime":null,"lastBillingTimeOfVMEvent":null,"lastBillingTimeOfMngtEvent":null}`, wantJSON: `{"creationTime":"2026-10-09T09:00:00Z"}`},
		{name: "empty resource ID", input: `{"resourceId":""}`, wantErr: true},
		{name: "malformed resource ID", input: `{"resourceId":"not-an-arm-id"}`, wantErr: true},
		{name: "object resource ID", input: `{"resourceId":{"name":"cluster"}}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc billingapi.BillingDocument
			err := json.Unmarshal([]byte(tc.input), &doc)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected decoding to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(&doc)
			if err != nil {
				t.Fatal(err)
			}
			var want, got map[string]any
			if err := json.Unmarshal([]byte(tc.wantJSON), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("wire format changed: got %s", encoded)
			}
		})
	}
}

func TestBillingDocumentDeepCopy(t *testing.T) {
	if (*billingapi.BillingDocument)(nil).DeepCopy() != nil {
		t.Fatal("DeepCopy of nil must be nil")
	}

	resourceID, err := azcorearm.ParseResourceID(testResourceID)
	if err != nil {
		t.Fatal(err)
	}
	moment := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	original := &billingapi.BillingDocument{
		ID:                         "billing-doc",
		DeletionTime:               &moment,
		LastBillingTimeOfVMEvent:   &moment,
		LastBillingTimeOfMngtEvent: &moment,
		ResourceID:                 resourceID,
	}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	copied := original.DeepCopy()
	*copied.DeletionTime = moment.Add(time.Hour)
	*copied.LastBillingTimeOfVMEvent = moment.Add(time.Hour)
	*copied.LastBillingTimeOfMngtEvent = moment.Add(time.Hour)
	copied.ResourceID.Name = "changed"
	copied.ResourceID.ResourceType.Types[0] = "changed"
	copied.ResourceID.Parent.Name = "changed"

	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("mutating the copy changed the original:\nbefore %s\nafter  %s", before, after)
	}
	if original.ResourceID.Name != "cluster" || original.ResourceID.ResourceType.Types[0] != "hcpOpenShiftClusters" || original.ResourceID.Parent.Name != "rg" {
		t.Fatal("mutating the copied resource ID changed the original")
	}
}

// DeepCopy clones only the reference fields listed here. If azcore adds another
// pointer, slice, map or similar field to its resource ID types, this fails so
// copyResourceID can be updated.
func TestResourceIDReferenceFieldsAreCopied(t *testing.T) {
	want := map[string]bool{"ResourceID.Parent": true, "ResourceType.Types": true}
	got := map[string]bool{}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			switch field.Type.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
				got[typ.Name()+"."+field.Name] = true
			case reflect.Struct:
				walk(field.Type)
			}
		}
	}
	walk(reflect.TypeOf(azcorearm.ResourceID{}))
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("resource ID reference fields = %v, want %v", got, want)
	}
}
