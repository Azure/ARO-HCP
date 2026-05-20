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

package kubeapplier

import (
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	unionlisterskubeapplier "github.com/Azure/ARO-HCP/internal/database/unionlisters/kubeapplier"
)

// UnionKubeApplierInformers is the union peer of
// informers.KubeApplierInformers. It exposes one (UnionDesireInformer, lister)
// pair per *Desire type, but each pair fans out across every
// per-management-cluster KubeApplierInformers that has been Added.
//
// The aggregator does not own sub-informer lifecycle: the caller starts
// each per-MC KubeApplierInformers with a child ctx (e.g. via
// RunWithContext) and pairs cancellation of that ctx with a Remove call.
// That layering keeps this type a pure registry — perfect substrate for a
// higher-level reactor that drives Add/Remove from a ManagementCluster
// informer.
//
// Add and Remove are serialized by mu so the six underlying sub-registrations
// (three informers, three listers) move together — concurrent callers can
// never observe a partially-Added or partially-Removed state.
type UnionKubeApplierInformers struct {
	mu sync.Mutex

	applyInformer  *UnionDesireInformer
	deleteInformer *UnionDesireInformer
	readInformer   *UnionDesireInformer

	applyLister  *unionlisterskubeapplier.UnionDesireLister[kubeapplier.ApplyDesire]
	deleteLister *unionlisterskubeapplier.UnionDesireLister[kubeapplier.DeleteDesire]
	readLister   *unionlisterskubeapplier.UnionDesireLister[kubeapplier.ReadDesire]
}

// NewUnionKubeApplierInformers returns an empty aggregator. Call Add to
// register per-management-cluster KubeApplierInformers.
func NewUnionKubeApplierInformers() *UnionKubeApplierInformers {
	return &UnionKubeApplierInformers{
		applyInformer:  NewUnionDesireInformer(),
		deleteInformer: NewUnionDesireInformer(),
		readInformer:   NewUnionDesireInformer(),

		applyLister:  unionlisterskubeapplier.NewUnionDesireLister[kubeapplier.ApplyDesire](),
		deleteLister: unionlisterskubeapplier.NewUnionDesireLister[kubeapplier.DeleteDesire](),
		readLister:   unionlisterskubeapplier.NewUnionDesireLister[kubeapplier.ReadDesire](),
	}
}

// ApplyDesires returns the union ApplyDesire informer and lister. Event
// handlers registered on the returned informer fan out to every
// per-management-cluster sub-informer (current and future).
func (u *UnionKubeApplierInformers) ApplyDesires() (*UnionDesireInformer, listers.ApplyDesireLister) {
	return u.applyInformer, u.applyLister
}

// DeleteDesires returns the union DeleteDesire informer and lister.
func (u *UnionKubeApplierInformers) DeleteDesires() (*UnionDesireInformer, listers.DeleteDesireLister) {
	return u.deleteInformer, u.deleteLister
}

// ReadDesires returns the union ReadDesire informer and lister.
func (u *UnionKubeApplierInformers) ReadDesires() (*UnionDesireInformer, listers.ReadDesireLister) {
	return u.readInformer, u.readLister
}

// Add registers a per-management-cluster KubeApplierInformers under the
// given resourceID. The sub's three informers and three listers are wired
// into the matching union informers and listers under u.mu, so concurrent
// callers see the registration atomically.
//
// Add does not start the sub-informer; the caller is responsible for that
// (typically by calling sub.RunWithContext on a per-MC ctx). Re-Add under
// the same resourceID replaces the previous registration on all six union
// surfaces.
//
// A nil resourceID or nil sub is a no-op. The error is non-nil only when a
// sub-informer rejects the union's previously-registered handlers; in that
// case the partial registration on the lister side is rolled back.
func (u *UnionKubeApplierInformers) Add(managementClusterResourceID *azcorearm.ResourceID, sub informers.KubeApplierInformers) error {
	if managementClusterResourceID == nil || sub == nil {
		return nil
	}
	applyInf, applyLister := sub.ApplyDesires()
	deleteInf, deleteLister := sub.DeleteDesires()
	readInf, readLister := sub.ReadDesires()

	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.applyInformer.Add(managementClusterResourceID, applyInf); err != nil {
		return err
	}
	if err := u.deleteInformer.Add(managementClusterResourceID, deleteInf); err != nil {
		u.applyInformer.Remove(managementClusterResourceID)
		return err
	}
	if err := u.readInformer.Add(managementClusterResourceID, readInf); err != nil {
		u.applyInformer.Remove(managementClusterResourceID)
		u.deleteInformer.Remove(managementClusterResourceID)
		return err
	}

	u.applyLister.Add(managementClusterResourceID, applyLister)
	u.deleteLister.Add(managementClusterResourceID, deleteLister)
	u.readLister.Add(managementClusterResourceID, readLister)
	return nil
}

// Remove deregisters the sub-informers and sublisters previously Added for
// the given resourceID across all six union surfaces. The sub-informers
// themselves are not stopped — the caller owns that lifecycle. Unknown or
// nil resourceID is a no-op.
func (u *UnionKubeApplierInformers) Remove(managementClusterResourceID *azcorearm.ResourceID) {
	if managementClusterResourceID == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	u.applyInformer.Remove(managementClusterResourceID)
	u.deleteInformer.Remove(managementClusterResourceID)
	u.readInformer.Remove(managementClusterResourceID)

	u.applyLister.Remove(managementClusterResourceID)
	u.deleteLister.Remove(managementClusterResourceID)
	u.readLister.Remove(managementClusterResourceID)
}

// HasSynced returns true only when every sub-informer (across all three
// *Desire types and every registered management cluster) has synced. An
// empty union is vacuously synced.
func (u *UnionKubeApplierInformers) HasSynced() bool {
	return u.applyInformer.HasSynced() && u.deleteInformer.HasSynced() && u.readInformer.HasSynced()
}
