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

package corecosmosstoragetesting

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
)

func TestMockResourcesChangeFeedStartPosition(t *testing.T) {
	ctx := t.Context()
	db := NewMockResourcesDBClient()
	old := []byte(`{"id":"old","_ts":1}`)
	recent := []byte(`{"id":"recent","_ts":1}`)
	before := time.Now()
	db.StoreDocument("old", old)
	db.StoreDocument("recent", recent)
	require.False(t, db.changeFeed.events[0].recordedAt.Before(before))
	require.False(t, db.changeFeed.events[1].recordedAt.After(time.Now()))
	// Fix event times to exercise the watcher's two-second overlap without sleeps.
	watchStart := time.Now().Truncate(time.Second)
	cutoff := watchStart.Add(-2 * time.Second)
	db.changeFeed.events[0].recordedAt = cutoff.Add(-time.Second)
	db.changeFeed.events[1].recordedAt = cutoff
	db.DeleteDocument("old")

	for _, tc := range []struct {
		name    string
		options *azcosmos.ChangeFeedOptions
		want    [][]byte
	}{
		{"nil options starts at beginning", nil, [][]byte{old, recent}},
		{"empty options starts at beginning", &azcosmos.ChangeFeedOptions{}, [][]byte{old, recent}},
		{"explicit beginning", &azcosmos.ChangeFeedOptions{StartFrom: metadataapihelpers.Ptr(time.Time{})}, [][]byte{old, recent}},
		{"time excludes old hard-deleted history", &azcosmos.ChangeFeedOptions{StartFrom: &cutoff}, [][]byte{recent}},
		{"time uses SDK whole-second precision", &azcosmos.ChangeFeedOptions{StartFrom: metadataapihelpers.Ptr(cutoff.Add(500 * time.Millisecond))}, [][]byte{recent}},
		{"empty continuation respects time", &azcosmos.ChangeFeedOptions{StartFrom: &cutoff, Continuation: metadataapihelpers.Ptr("")}, [][]byte{recent}},
		{"time after all events", &azcosmos.ChangeFeedOptions{StartFrom: &watchStart}, nil},
		{"continuation takes precedence over time", &azcosmos.ChangeFeedOptions{StartFrom: &watchStart, Continuation: metadataapihelpers.Ptr("1")}, [][]byte{recent}},
		{"zero continuation takes precedence over time", &azcosmos.ChangeFeedOptions{StartFrom: &watchStart, Continuation: metadataapihelpers.Ptr("0")}, [][]byte{old, recent}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := db.ReadChangeFeed(ctx, tc.options)
			require.NoError(t, err)
			require.Equal(t, tc.want, response.Items)
			require.EqualValues(t, "2", response.ETag)
			if len(tc.want) == 0 {
				require.Equal(t, http.StatusNotModified, response.RawResponse.StatusCode)
			} else {
				require.Equal(t, http.StatusOK, response.RawResponse.StatusCode)
			}
		})
	}

	// A 304 still supplies a cursor. Resuming it must read only subsequent writes,
	// even if StartFrom changes, rather than applying the time filter again.
	response, err := db.ReadChangeFeed(ctx, &azcosmos.ChangeFeedOptions{StartFrom: &watchStart})
	require.NoError(t, err)
	token, err := response.GetCompositeContinuationToken()
	require.NoError(t, err)
	newDocument := []byte(`{"id":"new","_ts":1}`)
	db.StoreDocument("new", newDocument)
	response, err = db.ReadChangeFeed(ctx, &azcosmos.ChangeFeedOptions{
		Continuation: &token,
		StartFrom:    metadataapihelpers.Ptr(watchStart.Add(time.Hour)),
	})
	require.NoError(t, err)
	require.Equal(t, [][]byte{newDocument}, response.Items)
	token, err = response.GetCompositeContinuationToken()
	require.NoError(t, err)
	response, err = db.ReadChangeFeed(ctx, &azcosmos.ChangeFeedOptions{Continuation: &token})
	require.NoError(t, err)
	require.Empty(t, response.Items)
	require.Equal(t, http.StatusNotModified, response.RawResponse.StatusCode)
}
