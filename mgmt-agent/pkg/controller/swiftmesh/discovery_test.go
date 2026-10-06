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

package swiftmesh

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

const swiftNetwork = "swiftv2-nic"

func routerPodObj(namespace, name, swiftIP string, phase corev1.PodPhase) *corev1.Pod {
	annotations := map[string]string{}
	if swiftIP != "" {
		annotations[MultusNetworkStatusAnnotation] = `[{"name":"` + namespace + `/` + swiftNetwork + `","interface":"net1","ips":["` + swiftIP + `"]}]`
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      map[string]string{"app": "router"},
			Annotations: annotations,
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func hcpNamespaceObj(name, resourceID string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if resourceID != "" {
		ns.Annotations = map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: resourceID}
	}
	return ns
}

func TestHCPDiscoverer(t *testing.T) {
	podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	nsIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	pods := []*corev1.Pod{
		// HCP "a": two running routers with SWIFT IPs.
		routerPodObj("ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1", "router-1", "10.100.77.5", corev1.PodRunning),
		routerPodObj("ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1", "router-2", "10.100.77.7", corev1.PodRunning),
		// HCP "a": a pending router (skipped) and a running router without a SWIFT IP (skipped).
		routerPodObj("ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1", "router-3", "10.100.77.9", corev1.PodPending),
		routerPodObj("ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1", "router-4", "", corev1.PodRunning),
		// HCP "b": one running router.
		routerPodObj("ocm-int-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-c2", "router-1", "10.100.88.5", corev1.PodRunning),
		// Non-HCP namespace (no resource-id annotation): skipped entirely.
		routerPodObj("some-other-namespace", "router-1", "10.100.99.5", corev1.PodRunning),
	}
	for _, p := range pods {
		if err := podIndexer.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, ns := range []*corev1.Namespace{
		hcpNamespaceObj("ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1", "/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/a"),
		hcpNamespaceObj("ocm-int-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-c2", "/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/b"),
		hcpNamespaceObj("some-other-namespace", ""), // not an HCP: no annotation.
	} {
		if err := nsIndexer.Add(ns); err != nil {
			t.Fatal(err)
		}
	}

	d := NewHCPDiscoverer(
		corelisters.NewPodLister(podIndexer),
		corelisters.NewNamespaceLister(nsIndexer),
		labels.SelectorFromSet(labels.Set{"app": "router"}),
		"router",
		swiftNetwork,
		8443,
	)

	meshes, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(meshes) != 2 {
		t.Fatalf("got %d meshes, want 2 (one per HCP, non-HCP namespace excluded)", len(meshes))
	}
	// Sorted by namespace: "a" before "b".
	a, b := meshes[0], meshes[1]
	if len(a.Routers) != 2 {
		t.Fatalf("HCP a: got %d routers, want 2 (pending and no-SWIFT-IP pods excluded)", len(a.Routers))
	}
	if a.Routers[0].Name != "router-1" || a.Routers[1].Name != "router-2" {
		t.Fatalf("HCP a routers not sorted by name: %+v", a.Routers)
	}
	if a.ResourceID == "" || a.Port != 8443 {
		t.Fatalf("HCP a: missing resource id or wrong port: %+v", a)
	}
	if len(b.Routers) != 1 || b.Routers[0].SwiftIP != "10.100.88.5" {
		t.Fatalf("HCP b: unexpected routers %+v", b.Routers)
	}
}
