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

package controllers

import (
	"context"
	"time"

	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dataDump struct {
	cosmosClient database.DBClient

	// nextDataDumpTime is a map of resourceID strings to a time at which all information related to them should be dumped.
	// This should work for any resource, though we're starting with Clusters because of coverage.  Every time we dump
	// we set the value forward by 10 minutes.  We only actually dump if an entry already exists in the LRU.  This prevents
	// us from spamming the log if we get super busy, but could be reconsidered if it doesn't work well.
	nextDataDumpTime *lru.Cache
}

// NewDataDumpController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDataDumpController(cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	c := &dataDump{
		cosmosClient:     cosmosClient,
		nextDataDumpTime: lru.New(10000),
	}

	return c
}

func (c *dataDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	resourceID := key.GetResourceID()
	if nextDataDumpTime, exists := c.nextDataDumpTime.Get(resourceID); !exists || time.Now().Before(nextDataDumpTime.(time.Time)) {
		return nil
	}
	defer c.nextDataDumpTime.Add(resourceID, time.Now().Add(5*time.Minute))

	if err := serverutils.DumpDataToLogger(ctx, c.cosmosClient, resourceID); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump data to logger")
	}

	return nil
}
