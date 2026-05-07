// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cincinnati

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCachingClient_CacheHit(t *testing.T) {
	cache := NewClientCache()
	clusterUUID := uuid.New()

	// First call creates client
	client1 := cache.GetOrCreateClient(clusterUUID)
	// Second call should return cached client (same instance)
	client2 := cache.GetOrCreateClient(clusterUUID)

	// Use == for interface comparison which checks pointer identity
	// when the underlying type is a pointer (*cincinnati.Client)
	assert.True(t, client1 == client2)
}

func TestCachingClient_DifferentUUIDs(t *testing.T) {
	cache := NewClientCache()

	uuid1 := uuid.New()
	uuid2 := uuid.New()

	client1 := cache.GetOrCreateClient(uuid1)
	client2 := cache.GetOrCreateClient(uuid2)

	// Use != for interface comparison which checks pointer identity
	assert.True(t, client1 != client2)

}
