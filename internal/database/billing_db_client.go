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
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// BillingDBClient provides access to the Cosmos DB Billing container (separate from ARM resource documents).
type BillingDBClient interface {
	BillingDocs(subscriptionID string) BillingDocCRUD
	BillingGlobalListers() BillingGlobalListers
}

// BillingGlobalListers exposes cross-partition listers for billing documents (for informers).
type BillingGlobalListers interface {
	BillingDocs() GlobalLister[BillingDocument]
}

type billingCosmosDBClient struct {
	billing *azcosmos.ContainerClient
}

var _ BillingDBClient = &billingCosmosDBClient{}

// NewBillingDBClient opens the Billing container on the given async database client.
func NewBillingDBClient(database *azcosmos.DatabaseClient) (BillingDBClient, error) {
	billing, err := database.NewContainer(billingContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return &billingCosmosDBClient{billing: billing}, nil
}

func (d *billingCosmosDBClient) BillingDocs(subscriptionID string) BillingDocCRUD {
	return NewBillingDocCRUD(d.billing, subscriptionID)
}

func (d *billingCosmosDBClient) BillingGlobalListers() BillingGlobalListers {
	return NewCosmosBillingGlobalListers(d.billing)
}

type cosmosBillingGlobalListers struct {
	billing *azcosmos.ContainerClient
}

var _ BillingGlobalListers = &cosmosBillingGlobalListers{}

// NewCosmosBillingGlobalListers builds BillingGlobalListers backed by the Billing container.
func NewCosmosBillingGlobalListers(billing *azcosmos.ContainerClient) BillingGlobalListers {
	return &cosmosBillingGlobalListers{billing: billing}
}

func (g *cosmosBillingGlobalListers) BillingDocs() GlobalLister[BillingDocument] {
	return &cosmosBillingGlobalLister{containerClient: g.billing}
}
