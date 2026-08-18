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

package corelisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// DNSReservationLister lists and gets DNSReservations from an informer's indexer.
type DNSReservationLister interface {
	List(ctx context.Context) ([]*coreapi.DNSReservation, error)
	Get(ctx context.Context, subscriptionID, dnsReservationName string) (*coreapi.DNSReservation, error)
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

func (l *dnsReservationLister) List(ctx context.Context) ([]*coreapi.DNSReservation, error) {
	return listerutils.ListAll[coreapi.DNSReservation](l.indexer)
}

// Get retrieves a single DNSReservation by subscription ID and DNS reservation name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<subscriptionID>/providers/microsoft.redhatopenshift/dnsreservations/<dnsReservationName>
func (l *dnsReservationLister) Get(ctx context.Context, subscriptionID, dnsReservationName string) (*coreapi.DNSReservation, error) {
	key := coreapi.ToDNSReservationResourceIDString(subscriptionID, dnsReservationName)
	return listerutils.GetByKey[coreapi.DNSReservation](l.indexer, key)
}
