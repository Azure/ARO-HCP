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

package database

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// LocksDBClient provides access to the Cosmos DB Locks container used for subscription-scoped concurrency.
type LocksDBClient interface {
	LockClient() LockClientInterface
}

type locksCosmosDBClient struct {
	lockClient *LockClient
}

var _ LocksDBClient = &locksCosmosDBClient{}

// NewLocksDBClient opens the Locks container on the given async database client and builds the lock client.
func NewLocksDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (LocksDBClient, error) {
	locks, err := database.NewContainer(locksContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	lockClient, err := NewLockClient(ctx, locks)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &locksCosmosDBClient{lockClient: lockClient}, nil
}

func (d *locksCosmosDBClient) LockClient() LockClientInterface {
	return d.lockClient
}
