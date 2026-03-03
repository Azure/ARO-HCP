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

package listers

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
)

// DNSReservationLister lists and gets DNS reservations from an informer's indexer.
type DNSReservationLister interface {
	List(ctx context.Context) ([]*api.DNSReservation, error)
	Get(ctx context.Context, subscriptionID, dnsReservationName string) (*api.DNSReservation, error)
}

// dnsReservationLister implements DNSReservationLister backed by a SharedIndexInformer.
type dnsReservationLister struct {
	indexer cache.Indexer
}

// NewDNSReservationLister creates a DNSReservationLister from a SharedIndexInformer's indexer.
func NewDNSReservationLister(indexer cache.Indexer) DNSReservationLister {
	return &dnsReservationLister{
		indexer: indexer,
	}
}

func (l *dnsReservationLister) List(ctx context.Context) ([]*api.DNSReservation, error) {
	return listAll[api.DNSReservation](l.indexer)
}

// Get retrieves a single DNS reservation by subscription ID and DNS reservation name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<subscriptionID>/providers/microsoft.redhatopenshift/dnsreservations/<dnsReservationName>
func (l *dnsReservationLister) Get(ctx context.Context, subscriptionID, dnsReservationName string) (*api.DNSReservation, error) {
	key := api.ToDNSReservationResourceIDString(subscriptionID, dnsReservationName)
	return getByKey[api.DNSReservation](l.indexer, key)
}
