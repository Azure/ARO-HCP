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

package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// Bound the Cosmos query so a hung request does not outlive the startup probe.
const cosmosProbeTimeout = 5 * time.Second

func newHealthzStartupHandler(logger logr.Logger, resourcesDBClient corecosmosstorage.ResourcesDBClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), cosmosProbeTimeout)
		defer cancel()
		if err := checkCosmos(ctx, resourcesDBClient); err != nil {
			logger.Error(err, "cosmos startup probe failed")
			http.Error(w, "cosmos container not queryable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func checkCosmos(ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) error {
	pageSizeHint := int32(1)
	iter, err := resourcesDBClient.ResourcesGlobalListers().Subscriptions().List(ctx,
		&cosmosstorageutils.DBClientListResourceDocsOptions{PageSizeHint: &pageSizeHint})
	if err != nil {
		return err
	}
	// Pulling the iterator executes the data-plane query. An empty page with
	// no error is healthy too; no subscription needs to exist for startup.
	for range iter.Items(ctx) {
		break
	}
	return iter.GetError()
}
