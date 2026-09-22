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

package nodemitigation

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
	recordfake "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned/fake"
)

type fixture struct {
	controller *Controller
	kube       *kubefake.Clientset
	records    *recordfake.Clientset
	now        time.Time
	cfg        Config
	informers  informers.SharedInformerFactory
}

func newFixture(t *testing.T, nodes int) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), cfg: testConfig()}
	objects := []runtime.Object{}
	for i := 0; i < nodes; i++ {
		name, instance := fmt.Sprintf("node-%02d", i), fmt.Sprintf("instance-%02d", i)
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), ResourceVersion: "1", CreationTimestamp: metav1.NewTime(f.now.Add(-time.Hour)),
			Labels: map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue, "kubernetes.azure.com/agentpool": "pool", corev1.LabelTopologyZone: "zone"}},
			Spec: corev1.NodeSpec{ProviderID: "azure://" + instance}, Status: corev1.NodeStatus{
				NodeInfo:    corev1.NodeSystemInfo{SystemUUID: instance},
				Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi"), corev1.ResourcePods: resource.MustParse("50"), kuberesources.SwiftNICResourceName: resource.MustParse("6")},
				Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}},
			}}
		objects = append(objects, node)
	}
	f.kube = kubefake.NewClientset(objects...)
	f.records = recordfake.NewSimpleClientset()
	eventNumber := 0
	f.kube.PrependReactor("create", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
		eventNumber++
		action.(ktesting.CreateAction).GetObject().(*corev1.Event).Name = fmt.Sprintf("event-%d", eventNumber)
		return false, nil, nil
	})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{mtpncGVR: "MultitenantPodNetworkConfigList"})
	f.informers = informers.NewSharedInformerFactory(f.kube, 0)
	var err error
	f.controller, err = NewController(f.kube, f.records, dyn, "mgmt-agent", f.informers.Core().V1().Nodes(), f.informers.Core().V1().Pods(), f.informers.Core().V1().Events(), func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	f.controller.AllowConfiguration(true)
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.controller.queue.ShutDown() })
	f.syncCaches(t)
	return f
}

func (f *fixture) syncCaches(t *testing.T) {
	t.Helper()
	for _, source := range []struct {
		resource, kind string
		store          cache.Store
	}{
		{"nodes", "Node", f.informers.Core().V1().Nodes().Informer().GetStore()},
		{"pods", "Pod", f.informers.Core().V1().Pods().Informer().GetStore()},
		{"events", "Event", f.informers.Core().V1().Events().Informer().GetStore()},
	} {
		list, err := f.kube.Tracker().List(corev1.SchemeGroupVersion.WithResource(source.resource), corev1.SchemeGroupVersion.WithKind(source.kind), "")
		if err != nil {
			t.Fatal(err)
		}
		items, err := meta.ExtractList(list)
		if err != nil {
			t.Fatal(err)
		}
		objects := make([]any, len(items))
		for i, item := range items {
			objects[i] = item
		}
		if err := source.store.Replace(objects, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	f.syncCaches(t)
	f.now = f.now.Add(2 * time.Second)
	if err := f.controller.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) ledger(t *testing.T) *api.NodeMitigationBudget {
	t.Helper()
	budget, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").Get(context.Background(), budgetName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return budget
}
func mutations(actions []ktesting.Action) []ktesting.Action {
	var result []ktesting.Action
	for _, action := range actions {
		if action.GetVerb() != "get" && action.GetVerb() != "list" && action.GetVerb() != "watch" {
			result = append(result, action)
		}
	}
	return result
}

func TestIdleDiscoveryAndReconcileRate(t *testing.T) {
	f := newFixture(t, 11)
	f.tick(t)
	f.kube.ClearActions()
	for i := 0; i < 10; i++ {
		f.tick(t)
	}
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "list" {
			t.Fatal("idle discovery performed live LIST")
		}
	}
	if delay := f.controller.reconcileDelay(); delay != 0 {
		t.Fatal(delay)
	}
	if delay := f.controller.reconcileDelay(); delay != f.cfg.RetryInterval.Duration {
		t.Fatal(delay)
	}
}
