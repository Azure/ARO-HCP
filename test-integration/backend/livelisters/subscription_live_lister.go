package livelisters

import (
	"context"

	"github.com/Azure/ARO-HCP/backend/listers"
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
