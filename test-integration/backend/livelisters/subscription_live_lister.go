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

package livelisters

import (
	"context"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewSubscriptionLiveLister is convenient for integration testing because it reads the live state from cosmos so there's no delay
// and no additional fake layer to synchronize.
func NewSubscriptionLiveLister(cosmosClient database.DBClient) listers.SubscriptionLister {
	return &subscriptionLiveLister{cosmosClient: cosmosClient}
}

type subscriptionLiveLister struct {
	cosmosClient database.DBClient
}

func (s *subscriptionLiveLister) HasSynced() bool {
	return true
}

func (s *subscriptionLiveLister) Get(ctx context.Context, name string) (*arm.Subscription, error) {
	return s.cosmosClient.Subscriptions().Get(ctx, name)
}

func (s *subscriptionLiveLister) List(ctx context.Context) ([]*arm.Subscription, error) {
	iterator, err := s.cosmosClient.Subscriptions().List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	ret := []*arm.Subscription{}
	for _, obj := range iterator.Items(ctx) {
		ret = append(ret, obj)
	}
	if err := iterator.GetError(); err != nil {
		return nil, utils.TrackError(err)
	}

	return ret, nil
}
