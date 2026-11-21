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

package billingcosmosstorage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

// TestBillingGlobalListers verifies that BillingGlobalListers exposes a billing document global lister.
func TestBillingGlobalListers(t *testing.T) {
	gl := &cosmosBillingGlobalListers{
		billing: nil,
	}
	lister := gl.BillingDocs()
	require.NotNil(t, lister, "BillingDocs() should return a non-nil cosmosstorageutils.GlobalLister")
}

func TestOrphanedSinceSerialization(t *testing.T) {
	observed := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		since            *time.Time
		wantJSON         string
		wantDocumentJSON string
	}{
		{name: "record candidate", since: &observed, wantJSON: `{"condition":"FROM c WHERE NOT IS_DEFINED(c.deletionTime) OR IS_NULL(c.deletionTime)","operations":[{"op":"set","path":"/orphanedSince","value":"2026-10-21T12:00:00Z"}]}`, wantDocumentJSON: `{"creationTime":"0001-01-01T00:00:00Z","orphanedSince":"2026-10-21T12:00:00Z"}`},
		{name: "clear candidate", wantJSON: `{"condition":"FROM c WHERE NOT IS_DEFINED(c.deletionTime) OR IS_NULL(c.deletionTime)","operations":[{"op":"set","path":"/orphanedSince","value":null}]}`, wantDocumentJSON: `{"creationTime":"0001-01-01T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var patch BillingDocumentPatchOperations
			patch.SetOrphanedSince(tc.since)
			payload, err := json.Marshal(patch)
			require.NoError(t, err)
			require.JSONEq(t, tc.wantJSON, string(payload))
			doc := &BillingDocument{OrphanedSince: tc.since}
			copy := doc.DeepCopy()
			if tc.since != nil {
				require.NotSame(t, doc.OrphanedSince, copy.OrphanedSince)
			}
			encoded, err := json.Marshal(copy)
			require.NoError(t, err)
			require.JSONEq(t, tc.wantDocumentJSON, string(encoded))
			var roundTrip BillingDocument
			require.NoError(t, json.Unmarshal(encoded, &roundTrip))
			if diff := cmp.Diff(tc.since, roundTrip.OrphanedSince); diff != "" {
				t.Fatalf("orphanedSince (-want +got):\n%s", diff)
			}
		})
	}
}
