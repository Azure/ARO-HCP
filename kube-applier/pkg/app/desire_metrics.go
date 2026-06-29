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

package app

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

const desireCollectInterval = 30 * time.Second

type desireCollector struct {
	applyStore  cache.Store
	deleteStore cache.Store
	readStore   cache.Store
	total       *prometheus.GaugeVec
}

func newDesireCollector(
	applyStore, deleteStore, readStore cache.Store,
	registerer prometheus.Registerer,
) *desireCollector {
	return &desireCollector{
		applyStore:  applyStore,
		deleteStore: deleteStore,
		readStore:   readStore,
		total: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kube_applier_desires",
				Help: "Number of desire objects where the given condition is True, by type and condition.",
			},
			[]string{"type", "condition"},
		),
	}
}

func (c *desireCollector) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		c.collect()
	}, desireCollectInterval)
}

func (c *desireCollector) collect() {
	counts := initCounts()

	for _, obj := range c.applyStore.List() {
		if d, ok := obj.(*kubeapplier.ApplyDesire); ok {
			countTrueConditions(counts["apply"], d.Status.Conditions)
		}
	}
	for _, obj := range c.deleteStore.List() {
		if d, ok := obj.(*kubeapplier.DeleteDesire); ok {
			countTrueConditions(counts["delete"], d.Status.Conditions)
		}
	}
	for _, obj := range c.readStore.List() {
		if d, ok := obj.(*kubeapplier.ReadDesire); ok {
			countTrueConditions(counts["read"], d.Status.Conditions)
		}
	}

	for desireType, condMap := range counts {
		for condType, n := range condMap {
			c.total.With(prometheus.Labels{
				"type":      desireType,
				"condition": condType,
			}).Set(n)
		}
	}
}

// initCounts seeds every known label combination to 0 so gauges go to zero
// when no desires with that condition are True, rather than going stale.
func initCounts() map[string]map[string]float64 {
	condTypes := []string{kubeapplier.ConditionTypeSuccessful, kubeapplier.ConditionTypeDegraded}
	counts := map[string]map[string]float64{}
	for _, t := range []string{"apply", "delete", "read"} {
		counts[t] = map[string]float64{}
		for _, cond := range condTypes {
			counts[t][cond] = 0
		}
	}
	return counts
}

func countTrueConditions(counts map[string]float64, conditions []metav1.Condition) {
	for _, cond := range conditions {
		if cond.Status == metav1.ConditionTrue {
			if _, ok := counts[cond.Type]; ok {
				counts[cond.Type]++
			}
		}
	}
}
