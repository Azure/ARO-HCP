package clusteractuator

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

func getAllActiveSubscriptions(ctx context.Context, dbClient database.DBClient) ([]*arm.Subscription, error) {
	iterator := dbClient.ListAllSubscriptionDocs()

	subscriptions := []*arm.Subscription{}
	for _, subscription := range iterator.Items(ctx) {
		// Unregistered subscriptions should have no active operations, not even deletes.
		if subscription.State == arm.SubscriptionStateUnregistered {
			continue
		}
		subscriptions = append(subscriptions, subscription)
	}

	err := iterator.GetError()
	if err != nil {
		return nil, err
	}

	return subscriptions, nil
}
