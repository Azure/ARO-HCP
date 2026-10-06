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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic/dynamiclister"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

func routerPodObj(namespace, name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "private-router"},
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

// mtpncObj builds an MTPNC named after its pod, carrying one vnet-NIC interface.
func mtpncObj(namespace, name, primaryIP string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("multitenancy.acn.azure.com/v1alpha1")
	u.SetKind("MultitenantPodNetworkConfig")
	u.SetNamespace(namespace)
	u.SetName(name)
	if err := unstructured.SetNestedSlice(u.Object, []any{
		map[string]any{"deviceType": vnetNICDeviceType, "primaryIP": primaryIP},
	}, "status", "interfaceInfos"); err != nil {
		panic(err)
	}
	return u
}

func newNamespacedIndexer() cache.Indexer {
	return cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
}

func TestHCPDiscoverer(t *testing.T) {
	const (
		nsA = "ocm-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c1"
		nsB = "ocm-int-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-c2"
	)

	podIndexer := newNamespacedIndexer()
	nsIndexer := newNamespacedIndexer()
	mtpncIndexer := newNamespacedIndexer()

	pods := []*corev1.Pod{
		routerPodObj(nsA, "router-1", corev1.PodRunning),                    // has MTPNC IP
		routerPodObj(nsA, "router-2", corev1.PodRunning),                    // has MTPNC IP
		routerPodObj(nsA, "router-3", corev1.PodPending),                    // skipped: not running
		routerPodObj(nsA, "router-4", corev1.PodRunning),                    // skipped: no MTPNC
		routerPodObj(nsB, "router-1", corev1.PodRunning),                    // has MTPNC IP
		routerPodObj("some-other-namespace", "router-1", corev1.PodRunning), // skipped: non-HCP ns
	}
	for _, p := range pods {
		if err := podIndexer.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, ns := range []*corev1.Namespace{
		hcpNamespaceObj(nsA, "/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/a"),
		hcpNamespaceObj(nsB, "/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/b"),
		hcpNamespaceObj("some-other-namespace", ""), // not an HCP: no annotation
	} {
		if err := nsIndexer.Add(ns); err != nil {
			t.Fatal(err)
		}
	}
	// MTPNCs exist for every running router EXCEPT nsA/router-4.
	for _, m := range []*unstructured.Unstructured{
		mtpncObj(nsA, "router-1", "10.100.77.5"),
		mtpncObj(nsA, "router-2", "10.100.77.7"),
		mtpncObj(nsB, "router-1", "10.100.88.5"),
		mtpncObj("some-other-namespace", "router-1", "10.100.99.5"),
	} {
		if err := mtpncIndexer.Add(m); err != nil {
			t.Fatal(err)
		}
	}

	d := NewHCPDiscoverer(
		corelisters.NewPodLister(podIndexer),
		corelisters.NewNamespaceLister(nsIndexer),
		dynamiclister.New(mtpncIndexer, MTPNCGroupVersionResource),
		labels.SelectorFromSet(labels.Set{"app": "private-router"}),
		"router",
		8443,
	)

	meshes, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(meshes) != 2 {
		t.Fatalf("got %d meshes, want 2 (one per HCP, non-HCP namespace excluded)", len(meshes))
	}
	a, b := meshes[0], meshes[1] // sorted by namespace
	if len(a.Routers) != 2 {
		t.Fatalf("HCP a: got %d routers, want 2 (pending and MTPNC-less pods excluded)", len(a.Routers))
	}
	if a.Routers[0].Name != "router-1" || a.Routers[0].SwiftIP != "10.100.77.5" {
		t.Fatalf("HCP a router-1 wrong: %+v", a.Routers[0])
	}
	if a.Routers[0].Container != "router" || a.Port != 8443 {
		t.Fatalf("HCP a: wrong container/port: %+v port=%d", a.Routers[0], a.Port)
	}
	if a.ResourceID == "" {
		t.Fatalf("HCP a: missing resource id")
	}
	if len(b.Routers) != 1 || b.Routers[0].SwiftIP != "10.100.88.5" {
		t.Fatalf("HCP b: unexpected routers %+v", b.Routers)
	}
}

func TestSwiftIPFromMTPNC(t *testing.T) {
	mk := func(interfaces []any, deprecatedIP string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{}}
		status := map[string]any{}
		if interfaces != nil {
			status["interfaceInfos"] = interfaces
		}
		if deprecatedIP != "" {
			status["primaryIP"] = deprecatedIP
		}
		u.Object["status"] = status
		return u
	}

	t.Run("prefers vnet NIC over infiniband", func(t *testing.T) {
		u := mk([]any{
			map[string]any{"deviceType": "acn.azure.com/infiniband-nic", "primaryIP": "10.0.0.99"},
			map[string]any{"deviceType": vnetNICDeviceType, "primaryIP": "10.100.77.5"},
		}, "")
		if ip, ok := swiftIPFromMTPNC(u); !ok || ip != "10.100.77.5" {
			t.Fatalf("got (%q,%v), want (10.100.77.5,true)", ip, ok)
		}
	})
	t.Run("strips CIDR mask", func(t *testing.T) {
		u := mk([]any{map[string]any{"deviceType": vnetNICDeviceType, "primaryIP": "10.100.77.5/32"}}, "")
		if ip, ok := swiftIPFromMTPNC(u); !ok || ip != "10.100.77.5" {
			t.Fatalf("got (%q,%v), want (10.100.77.5,true)", ip, ok)
		}
	})
	t.Run("falls back to any interface when no vnet NIC", func(t *testing.T) {
		u := mk([]any{map[string]any{"deviceType": "acn.azure.com/other", "primaryIP": "10.1.2.3"}}, "")
		if ip, ok := swiftIPFromMTPNC(u); !ok || ip != "10.1.2.3" {
			t.Fatalf("got (%q,%v), want (10.1.2.3,true)", ip, ok)
		}
	})
	t.Run("falls back to deprecated top-level primaryIP", func(t *testing.T) {
		if ip, ok := swiftIPFromMTPNC(mk(nil, "10.9.9.9")); !ok || ip != "10.9.9.9" {
			t.Fatalf("got (%q,%v), want (10.9.9.9,true)", ip, ok)
		}
	})
	t.Run("no IP available", func(t *testing.T) {
		if ip, ok := swiftIPFromMTPNC(mk([]any{map[string]any{"deviceType": vnetNICDeviceType, "primaryIP": ""}}, "")); ok {
			t.Fatalf("expected no IP, got %q", ip)
		}
	})

	// Exact shape of a live production MTPNC (extra fields present, primaryIP has
	// a /32 mask, no status.status set).
	t.Run("real production object", func(t *testing.T) {
		u := mk([]any{map[string]any{
			"gatewayIP":          "10.151.166.49",
			"macAddress":         "60:45:bd:b5:27:78",
			"ncID":               "ed846f38-be00-4421-b9c8-857f1de45f69",
			"primaryIP":          "10.151.166.54/32",
			"accelnetEnabled":    true,
			"deviceType":         "acn.azure.com/vnet-nic",
			"subnetAddressSpace": "10.151.166.48/28",
		}}, "10.151.166.54/32")
		if ip, ok := swiftIPFromMTPNC(u); !ok || ip != "10.151.166.54" {
			t.Fatalf("got (%q,%v), want (10.151.166.54,true)", ip, ok)
		}
	})
}
