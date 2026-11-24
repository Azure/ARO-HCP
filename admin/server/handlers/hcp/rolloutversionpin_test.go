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

package hcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/errorutils"
)

type rolloutPinTestClient struct {
	fleetcosmosstorage.FleetDBClient
	crud *rolloutPinTestCRUD
}

func (c *rolloutPinTestClient) ControlPlaneVersionRollouts() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout] {
	return c.crud
}

type rolloutPinTestCRUD struct {
	writeError bool
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
	conflict   bool
	readError  bool
	concurrent *fleetapi.ControlPlaneVersionRollout
}

func (c *rolloutPinTestCRUD) Replace(ctx context.Context, obj, old *fleetapi.ControlPlaneVersionRollout, options *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
	if c.writeError {
		return nil, fmt.Errorf("injected write failure")
	}
	return c.ValidatingResourceCRUD.Replace(ctx, obj, old, options)
}

func (c *rolloutPinTestCRUD) Get(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
	if c.readError {
		return nil, fmt.Errorf("injected read failure")
	}
	existing, err := c.ValidatingResourceCRUD.Get(ctx, name)
	if err != nil || !c.conflict {
		return existing, err
	}
	update := existing.DeepCopy()
	update.Spec.BestExactVersion = ptr.To(semver.MustParse("4.22.9"))
	c.concurrent, err = c.Replace(ctx, update, existing, nil)
	return existing, err
}

func TestRolloutVersionPinHandler(t *testing.T) {
	for _, tc := range []struct {
		name, body, channel, response            string
		status                                   int
		missing, conflict, readError, writeError bool
	}{
		{name: "set", body: `{"exactVersion":"4.22.2","untilExactVersion":"4.22.8"}`, response: `{"exactVersion":"4.22.2","untilExactVersion":"4.22.8"}`},
		{name: "indefinite", body: `{"exactVersion":"4.22.2"}`, response: `{"exactVersion":"4.22.2"}`},
		{name: "nightly exact", body: `{"exactVersion":"4.22.0-0.nightly-2026-11-15-123456","untilExactVersion":"4.22.8"}`, response: `{"exactVersion":"4.22.0-0.nightly-2026-11-15-123456","untilExactVersion":"4.22.8"}`},
		{name: "clear", body: `{}`, response: `{}`},
		{name: "null field clears", body: `{"exactVersion":null}`, response: `{}`},
		{name: "missing", body: `{}`, missing: true, status: 404},
		{name: "conflict set", body: `{"exactVersion":"4.22.2"}`, conflict: true, status: 409},
		{name: "conflict clear", body: `{}`, conflict: true, status: 409},
		{name: "write failure", body: `{}`, writeError: true, status: 500},
		{name: "read failure", body: `{}`, readError: true, status: 500},
		{name: "invalid channel", channel: "stable-invalid", body: `{}`, status: 400},
		{name: "wrong minor", body: `{"exactVersion":"4.23.2"}`, status: 400},
		{name: "until without exact", body: `{"untilExactVersion":"4.22.8"}`, status: 400},
		{name: "lower until", body: `{"exactVersion":"4.22.8","untilExactVersion":"4.22.2"}`, status: 400},
		{name: "wrong until minor", body: `{"exactVersion":"4.22.2","untilExactVersion":"4.23.8"}`, status: 400},
		{name: "invalid exact", body: `{"exactVersion":"bad"}`, status: 400},
		{name: "empty exact", body: `{"exactVersion":""}`, status: 400},
		{name: "invalid until", body: `{"exactVersion":"4.22.2","untilExactVersion":"bad"}`, status: 400},
		{name: "build metadata", body: `{"exactVersion":"4.22.2+build"}`, status: 400},
		{name: "unknown field", body: `{"exactVerison":"4.22.2"}`, status: 400},
		{name: "empty body", body: "", status: 400},
		{name: "null body", body: "null", status: 400},
		{name: "array", body: "[]", status: 400},
		{name: "trailing value", body: "{} {}", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-4.22")
			require.NoError(t, err)
			rollout := &fleetapi.ControlPlaneVersionRollout{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
				Spec:           fleetapi.ControlPlaneVersionRolloutSpec{Version: coreapi.VersionProfile{ID: "4.22", ChannelGroup: "stable"}, BestExactVersion: ptr.To(semver.MustParse("4.22.6")), PinnedVersion: fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: ptr.To(semver.MustParse("4.22.1"))}},
				Status:         fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.22.6": 3}},
			}
			resources := []any{}
			if !tc.missing {
				resources = append(resources, rollout)
			}
			db, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			storage := db.ControlPlaneVersionRollouts()
			before, _ := storage.Get(ctx, "stable-4.22")
			crud := &rolloutPinTestCRUD{ValidatingResourceCRUD: storage, conflict: tc.conflict, readError: tc.readError, writeError: tc.writeError}
			client := &rolloutPinTestClient{FleetDBClient: db, crud: crud}
			mux := http.NewServeMux()
			mux.Handle("POST /admin/v1/versionrollouts/{channel}/pinnedversion", errorutils.ReportError(NewRolloutVersionPinHandler(client).ServeHTTP))
			channel := tc.channel
			if channel == "" {
				channel = "stable-4.22"
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/v1/versionrollouts/"+channel+"/pinnedversion", strings.NewReader(tc.body)))
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			require.Equal(t, status, recorder.Code, recorder.Body.String())
			after, err := storage.Get(ctx, "stable-4.22")
			if tc.missing {
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
				return
			}
			require.NoError(t, err)
			if tc.conflict {
				require.Equal(t, crud.concurrent, after)
				return
			}
			if status != http.StatusOK {
				require.Equal(t, before, after)
				return
			}
			require.JSONEq(t, tc.response, recorder.Body.String())
			expected := before.DeepCopy()
			expected.Spec.PinnedVersion = after.Spec.PinnedVersion
			expected.CosmosETag = after.CosmosETag
			expected.InstanceVersion = before.InstanceVersion + 1
			require.Equal(t, expected, after)
			var expectedPin fleetapi.ServiceProviderClusterPinnedVersion
			require.NoError(t, json.Unmarshal([]byte(tc.response), &expectedPin))
			require.Equal(t, expectedPin, after.Spec.PinnedVersion)

		})
	}
}
