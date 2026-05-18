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

package informers

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// KubeApplierInformers bundles one SharedIndexInformer per *Desire type plus the
// matching listers. Both the kube-applier binary (single-partition) and the
// backend (cross-partition) construct one of these with the appropriate
// database.KubeApplierGlobalListers — the factory does not care which.
type KubeApplierInformers interface {
	ApplyDesires() (cache.SharedIndexInformer, listers.ApplyDesireLister)
	DeleteDesires() (cache.SharedIndexInformer, listers.DeleteDesireLister)
	ReadDesires() (cache.SharedIndexInformer, listers.ReadDesireLister)

	// RunWithContext starts every informer and blocks until ctx is cancelled.
	RunWithContext(ctx context.Context)
}

type kubeApplierInformers struct {
	applyDesireInformer cache.SharedIndexInformer
	applyDesireLister   listers.ApplyDesireLister

	deleteDesireInformer cache.SharedIndexInformer
	deleteDesireLister   listers.DeleteDesireLister

	readDesireInformer cache.SharedIndexInformer
	readDesireLister   listers.ReadDesireLister
}

func (k *kubeApplierInformers) ApplyDesires() (cache.SharedIndexInformer, listers.ApplyDesireLister) {
	return k.applyDesireInformer, k.applyDesireLister
}

func (k *kubeApplierInformers) DeleteDesires() (cache.SharedIndexInformer, listers.DeleteDesireLister) {
	return k.deleteDesireInformer, k.deleteDesireLister
}

func (k *kubeApplierInformers) ReadDesires() (cache.SharedIndexInformer, listers.ReadDesireLister) {
	return k.readDesireInformer, k.readDesireLister
}

// NewKubeApplierInformers wires up the three *Desire informers + listers using
// the default relist durations. The kube-applier binary calls this with
// client.PartitionListers(mgmtCluster); the backend calls it with
// client.GlobalListers().
func NewKubeApplierInformers(
	ctx context.Context, gl database.KubeApplierGlobalListers,
) KubeApplierInformers {
	return NewKubeApplierInformersWithRelistDuration(ctx, gl, nil)
}

// NewKubeApplierInformersWithRelistDuration is the same as NewKubeApplierInformers
// but lets the caller override the relist duration uniformly across all three
// informers. Tests use this to drive faster relists.
func NewKubeApplierInformersWithRelistDuration(
	ctx context.Context, gl database.KubeApplierGlobalListers, relistDuration *time.Duration,
) KubeApplierInformers {
	apply := ApplyDesireRelistDuration
	delete := DeleteDesireRelistDuration
	read := ReadDesireRelistDuration
	if relistDuration != nil {
		apply = *relistDuration
		delete = *relistDuration
		read = *relistDuration
	}

	ret := &kubeApplierInformers{}
	ret.applyDesireInformer = NewApplyDesireInformerWithRelistDuration(gl.ApplyDesires(), apply)
	ret.deleteDesireInformer = NewDeleteDesireInformerWithRelistDuration(gl.DeleteDesires(), delete)
	ret.readDesireInformer = NewReadDesireInformerWithRelistDuration(gl.ReadDesires(), read)

	ret.applyDesireLister = listers.NewApplyDesireLister(ret.applyDesireInformer.GetIndexer())
	ret.deleteDesireLister = listers.NewDeleteDesireLister(ret.deleteDesireInformer.GetIndexer())
	ret.readDesireLister = listers.NewReadDesireLister(ret.readDesireInformer.GetIndexer())

	return ret
}

func (k *kubeApplierInformers) RunWithContext(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting kube-applier informers")
	defer logger.Info("stopped kube-applier informers")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		k.applyDesireInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		k.deleteDesireInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		k.readDesireInformer.RunWithContext(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}
