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

package routercheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

func discoveryClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		machineGVR: "MachineList", azureMachineGVR: "AzureMachineList", routeGVR: "RouteList", mtpncGVR: "MultitenantPodNetworkConfigList",
	}, objects...)
}

func testDiscoverySources(t *testing.T, client kubernetes.Interface, dynamicClient dynamic.Interface, unsynced ...string) *discoverySources {
	t.Helper()
	s := newDiscoverySources(client, dynamicClient)
	ctx, cancel := context.WithCancel(t.Context())
	s.Start(ctx.Done())
	t.Cleanup(func() { cancel(); s.Shutdown() })
	checks := map[string]cache.InformerSynced{"pods": s.pods.Informer().HasSynced, "services": s.services.Informer().HasSynced, "endpointslices": s.slices.Informer().HasSynced,
		"secrets": s.secrets.Informer().HasSynced, "configmaps": s.configMaps.Informer().HasSynced}
	for gvr, informer := range s.objects {
		checks[gvr.Resource] = informer.Informer().HasSynced
	}
	var syncs []cache.InformerSynced
	for name, check := range checks {
		if !slices.Contains(unsynced, name) {
			syncs = append(syncs, check)
		}
	}
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if !cache.WaitForCacheSync(ctx.Done(), syncs...) {
		t.Fatal("discovery informers did not sync")
	}
	return s
}

func trustFixtures() []runtime.Object {
	return []runtime.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ignitionCAName, Namespace: "ns"}, Data: map[string][]byte{corev1.TLSCertKey: []byte("ignition-public-ca")}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: rootCAName, Namespace: "ns"}, Data: map[string]string{"ca.crt": "root-public-ca"}},
	}
}

func routerPod(name, uid, node, ip string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(uid), Labels: map[string]string{"app": "private-router", pniLabel: "pni"}},
		Spec:   corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "router", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{swiftResource: resource.MustParse("1")}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
}

func mtpnc(pod *corev1.Pod, ip string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "multitenancy.acn.azure.com/v1alpha1", "kind": "MultitenantPodNetworkConfig",
		"metadata": map[string]any{"name": pod.Name, "namespace": pod.Namespace},
		"spec":     map[string]any{"podName": pod.Name, "podUID": string(pod.UID)},
		"status":   map[string]any{"nodeName": pod.Spec.NodeName, "interfaceInfos": []any{map[string]any{"deviceType": "acn.azure.com/vnet-nic", "primaryIP": ip, "macAddress": "00:11:22:33:44:55"}}}}}
}

func decodedMTPNC(t *testing.T, pod *corev1.Pod, ip string) *MTPNC {
	t.Helper()
	var result MTPNC
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(mtpnc(pod, ip).Object, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func kasFixtures() (*corev1.Service, *discoveryv1.EndpointSlice, *unstructured.Unstructured) {
	owner := metav1.OwnerReference{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedControlPlane", Name: "platform", UID: "hcp-uid"}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{owner}}, Spec: corev1.ServiceSpec{
		ClusterIP: "172.16.0.3", Selector: map[string]string{"app": "kube-apiserver"}, Ports: []corev1.ServicePort{{Name: "client", Port: 6443, TargetPort: intstr.FromString("client")}},
	}}
	endpoints := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "kas", Namespace: "ns", Labels: map[string]string{discoveryv1.LabelServiceName: svc.Name}}, AddressType: discoveryv1.AddressTypeIPv4,
		Ports: []discoveryv1.EndpointPort{{Name: ptr.To("client"), Port: ptr.To(int32(7443))}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.4.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(false)}}},
	}
	route := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "route.openshift.io/v1", "kind": "Route", "metadata": map[string]any{"name": "kube-apiserver-internal", "namespace": "ns"}, "spec": map[string]any{
		"host": "api.platform.hypershift.local", "to": map[string]any{"kind": "Service", "name": svc.Name}, "tls": map[string]any{"termination": "passthrough"},
	}}}
	route.SetOwnerReferences([]metav1.OwnerReference{owner})
	route.SetLabels(map[string]string{"hypershift.openshift.io/hosted-control-plane": "ns"})
	return svc, endpoints, route
}

func discoveryInput(t *testing.T) DiscoveryInput {
	t.Helper()
	pod := routerPod("router", "uid", "node", "10.0.0.1")
	peer := routerPod("peer", "peer-uid", "other-node", "10.0.0.2")
	peer.DeletionTimestamp = ptr.To(metav1.Now())
	peer.Status.Conditions[0].Status = corev1.ConditionFalse
	svc, endpoints, route := kasFixtures()
	var kasRoute routev1.Route
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(route.Object, &kasRoute); err != nil {
		t.Fatal(err)
	}
	input := DiscoveryInput{Pod: pod, DNS: probe.DNSConfig{Servers: []string{"10.0.0.10"}, Searches: []string{"ns.svc.custom.test"}},
		IgnitionRoute: &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: "ignition-server", Namespace: "ns", Labels: map[string]string{"hypershift.openshift.io/hosted-control-plane": "ns"}}, Spec: routev1.RouteSpec{Host: "ignition.example.test"}},
		KASRoutes:     []*routev1.Route{&kasRoute}, LocalMTPNC: decodedMTPNC(t, pod, "10.1.0.1/24"), Peers: []RouterInput{{Pod: peer, MTPNC: decodedMTPNC(t, peer, "10.1.0.2/24")}},
		TrustBundles: map[string]string{probe.TrustIgnition: "ignition-public-ca", probe.TrustRoot: "root-public-ca"}, Workers: []WorkerInput{workerInput("worker", "infra", "10.2.0.1")}}
	for i, name := range []string{"ignition-server", "ignition-server-proxy", "kube-apiserver"} {
		portName, port := "https", int32(9090+i)
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"}, Spec: corev1.ServiceSpec{ClusterIP: fmt.Sprintf("172.16.0.%d", i+1), Selector: map[string]string{"app": name}, Ports: []corev1.ServicePort{{Name: portName, Port: 443, TargetPort: intstr.FromString(portName)}}}}
		slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: map[string]string{discoveryv1.LabelServiceName: name}}, AddressType: discoveryv1.AddressTypeIPv4,
			Ports: []discoveryv1.EndpointPort{{Name: ptr.To(portName), Port: ptr.To(port)}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{fmt.Sprintf("10.3.0.%d", i+1)}, Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(false)}}}}
		if name == "kube-apiserver" {
			service, slice, portName, port = svc, endpoints, "client", 7443
		}
		backend := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name + "-uid"), Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{NodeName: "backend-node", Containers: []corev1.Container{{Ports: []corev1.ContainerPort{{Name: portName, ContainerPort: port}}}}}}
		slice.Endpoints[0].TargetRef = &corev1.ObjectReference{Kind: "Pod", Name: backend.Name, UID: backend.UID}
		input.Services = append(input.Services, ServiceInput{Service: service, Pods: []*corev1.Pod{backend}, Slices: []*discoveryv1.EndpointSlice{slice}})
	}
	return input
}

func TestBuildDiscovery(t *testing.T) {
	input := discoveryInput(t)
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	d, err := BuildDiscovery(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Targets) != 18 {
		t.Fatalf("unexpected target count: %d", len(d.Targets))
	}
	counts := map[string]int{}
	for _, info := range d.Targets {
		target := info.Target
		counts[target.Role]++
		switch {
		case target.Role == "worker-outbound":
			if !target.TCPOnly || target.SourceIP != "10.1.0.1" {
				t.Fatalf("worker binding: %+v", target)
			}
		case target.Role == "haproxy-ready":
			if !target.PlainHTTP || target.ServerName != "" || target.Path != "/haproxy_ready" {
				t.Fatalf("readiness: %+v", target)
			}
		case strings.HasPrefix(target.Role, "kas-"):
			if target.ServerName != "api.platform.hypershift.local" || target.TrustBundle != probe.TrustRoot || target.Path != "/readyz" || !target.LivenessOnFailure {
				t.Fatalf("KAS contract: %+v", target)
			}
		case strings.HasPrefix(target.Role, "ignition-server-") && !strings.HasPrefix(target.Role, "ignition-server-proxy-"):
			if target.ServerName != "ignition-server.ns.svc" || target.TrustBundle != probe.TrustRoot {
				t.Fatalf("direct ignition trust: %+v", target)
			}
		default:
			if target.ServerName != "ignition.example.test" || target.TrustBundle != probe.TrustIgnition {
				t.Fatalf("router trust: %+v", target)
			}
		}
		if target.Role == "peer-swift" && (target.SourceIP != "10.1.0.1" || !info.Deleting || info.Ready == nil || *info.Ready) {
			t.Fatalf("peer evidence: %+v", info)
		}
		if strings.HasSuffix(target.Role, "-endpoint") && (info.SelectorMatches == nil || !*info.SelectorMatches || info.TargetPortMatches == nil || !*info.TargetPortMatches || info.Node != "backend-node") {
			t.Fatalf("endpoint evidence: %+v", info)
		}
	}
	if len(counts) != 18 {
		t.Fatalf("missing roles: %v", counts)
	}
	if !slices.Equal(d.DNSNames, []string{"ignition-server.ns.svc.custom.test.", "ignition-server-proxy.ns.svc.custom.test.", "kube-apiserver.ns.svc.custom.test."}) {
		t.Fatalf("DNS: %v", d.DNSNames)
	}
	again, err := BuildDiscovery(input)
	if err != nil || !reflect.DeepEqual(d, again) {
		t.Fatalf("non-deterministic build: %v", err)
	}
	// Mutating output must not mutate informer-owned input pointers or maps.
	d.TrustBundles[probe.TrustRoot] = "changed"
	for i := range d.Targets {
		if d.Targets[i].Ready != nil {
			*d.Targets[i].Ready = true
		}
		if d.Targets[i].Ref != nil {
			d.Targets[i].Ref.Name = "changed"
		}
	}
	after, err := json.Marshal(input)
	if err != nil || string(before) != string(after) {
		t.Fatalf("input mutated: %v", err)
	}
}

func TestBuildDiscoveryMissingData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DiscoveryInput)
	}{
		{"route", func(i *DiscoveryInput) { i.IgnitionRoute = nil }},
		{"route host", func(i *DiscoveryInput) { i.IgnitionRoute.Spec.Host = "" }},
		{"KAS routes", func(i *DiscoveryInput) { i.KASRoutes = nil }},
		{"local Swift", func(i *DiscoveryInput) { i.LocalMTPNC = nil }},
		{"peer Swift", func(i *DiscoveryInput) { i.Peers[0].MTPNC = nil }},
		{"peer family", func(i *DiscoveryInput) { i.Peers[0].MTPNC.Status.InterfaceInfos[0].PrimaryIP = "fd00::2" }},
		{"management address", func(i *DiscoveryInput) { i.Pod.Status.PodIP = "" }},
		{"peer management address", func(i *DiscoveryInput) { i.Peers[0].Pod.Status.PodIP = "" }},
		{"DNS servers", func(i *DiscoveryInput) { i.DNS.Servers = nil }},
		{"DNS domain", func(i *DiscoveryInput) { i.DNS.Searches = nil }},
		{"service", func(i *DiscoveryInput) { i.Services = i.Services[1:] }},
		{"pods", func(i *DiscoveryInput) { i.Services[0].Pods = nil }},
		{"slices", func(i *DiscoveryInput) { i.Services[0].Slices = nil }},
		{"endpoints", func(i *DiscoveryInput) { i.Services[0].Slices[0].Endpoints = nil }},
		{"endpoint addresses", func(i *DiscoveryInput) { i.Services[0].Slices[0].Endpoints[0].Addresses = nil }},
		{"endpoint port", func(i *DiscoveryInput) { i.Services[0].Slices[0].Ports = nil }},
		{"service port", func(i *DiscoveryInput) { i.Services[0].Service.Spec.Ports = nil }},
		{"service IP", func(i *DiscoveryInput) { i.Services[0].Service.Spec.ClusterIP = "" }},
		{"workers", func(i *DiscoveryInput) { i.Workers = nil }},
		{"worker address", func(i *DiscoveryInput) { i.Workers[0].AzureMachine.Status.Addresses = nil }},
		{"worker family", func(i *DiscoveryInput) { i.Workers[0].AzureMachine.Status.Addresses[0].Address = "fd00::1" }},
		{"trust", func(i *DiscoveryInput) { delete(i.TrustBundles, probe.TrustRoot) }},
		{"oversized trust", func(i *DiscoveryInput) { i.TrustBundles[probe.TrustRoot] = strings.Repeat("x", maxTrustBundleBytes+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := discoveryInput(t)
			tc.change(&input)
			d, err := BuildDiscovery(input)
			if !errors.Is(err, ErrDiscoveryData) || !reflect.DeepEqual(d, Discovery{}) {
				t.Fatalf("partial discovery: %+v, error=%v", d, err)
			}
		})
	}
}

func TestKASDiscovery(t *testing.T) {
	for _, scenario := range []string{"complete", "unnamed port", "external", "private", "wrong uid", "wrong label", "wrong host", "wrong backend", "wrong owner group", "missing owner", "ignition owner mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			input := discoveryInput(t)
			route := input.KASRoutes[0]
			valid := false
			switch scenario {
			case "complete":
				valid = true
			case "unnamed port":
				input.Services[2].Service.Spec.Ports[0].Name = ""
				input.Services[2].Slices[0].Ports[0].Name = ptr.To("")
				valid = true
			case "external", "private":
				route.Name = "kube-apiserver"
				if scenario == "private" {
					route.Name += "-private"
				}
				route.Spec.Host = "customer.example.com"
				valid = true
			case "wrong uid":
				route.OwnerReferences[0].UID = "other"
			case "wrong label":
				route.Labels["hypershift.openshift.io/hosted-control-plane"] = "other"
			case "wrong host":
				route.Spec.Host = "api.other.hypershift.local"
			case "wrong backend":
				route.Spec.To.Name = "other"
			case "wrong owner group":
				input.Services[2].Service.OwnerReferences[0].APIVersion = "other.io/v1"
			case "missing owner":
				input.Services[2].Service.OwnerReferences = nil
			case "ignition owner mismatch":
				input.IgnitionRoute.OwnerReferences = []metav1.OwnerReference{{Kind: "HostedControlPlane", APIVersion: "hypershift.openshift.io/v1beta1", Name: "other", UID: "other"}}
			}
			d, err := BuildDiscovery(input)
			if (err == nil) != valid {
				t.Fatalf("valid=%t, error=%v", valid, err)
			}
			for _, info := range d.Targets {
				if strings.HasPrefix(info.Target.Role, "kas-") && info.Target.ServerName != "api.platform.hypershift.local" {
					t.Fatalf("customer SNI fallback: %+v", info)
				}
			}
		})
	}
}

func TestSwiftInterfaces(t *testing.T) {
	for _, scenario := range []string{"current", "legacy", "empty interfaces", "bad uid", "bad owner", "bad node", "bad ip", "wrong device", "no node"} {
		t.Run(scenario, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			mtpnc := decodedMTPNC(t, pod, "10.1.0.4/24")
			switch scenario {
			case "legacy":
				mtpnc.Status.InterfaceInfos = nil
				mtpnc.Status.PrimaryIP = "10.1.0.4"
			case "empty interfaces":
				mtpnc.Status.InterfaceInfos = []MTPNCInterface{}
				mtpnc.Status.PrimaryIP = "10.1.0.4"
			case "bad uid":
				mtpnc.Spec.PodUID = "old"
			case "bad owner":
				mtpnc.OwnerReferences = []metav1.OwnerReference{{Kind: "Pod", Name: pod.Name, UID: "old"}}
			case "bad node":
				mtpnc.Status.NodeName = "other"
			case "bad ip":
				mtpnc.Status.InterfaceInfos[0].PrimaryIP = "invalid"
			case "wrong device":
				mtpnc.Status.InterfaceInfos[0].DeviceType = "other"
			case "no node":
				mtpnc.Status.NodeName = ""
			}
			got, err := swiftInterfaces(mtpnc, pod)
			valid := scenario == "current" || scenario == "legacy" || scenario == "no node"
			if (err == nil) != valid || (valid && (len(got) != 1 || got[0].IP != "10.1.0.4")) {
				t.Fatalf("interfaces=%+v err=%v", got, err)
			}
		})
	}
}

// cachedDiscoveryInput uses standard informer indexers, without starting any
// watches. Gather must read listers, not poll APIs or re-check HasSynced.
func cachedDiscoveryInput(t *testing.T, input DiscoveryInput) *discoverySources {
	t.Helper()
	s := newDiscoverySources(fake.NewClientset(), discoveryClient())
	add := func(indexer cache.Indexer, object any) {
		t.Helper()
		if err := indexer.Add(object); err != nil {
			t.Fatal(err)
		}
	}
	addDynamic := func(gvr schema.GroupVersionResource, object any) {
		t.Helper()
		data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
		if err != nil {
			t.Fatal(err)
		}
		add(s.objects[gvr].Informer().GetIndexer(), &unstructured.Unstructured{Object: data})
	}
	addDynamic(routeGVR, input.IgnitionRoute)
	for _, route := range input.KASRoutes {
		addDynamic(routeGVR, route)
	}
	addDynamic(mtpncGVR, input.LocalMTPNC)
	add(s.pods.Informer().GetIndexer(), input.Pod)
	for _, peer := range input.Peers {
		add(s.pods.Informer().GetIndexer(), peer.Pod)
		addDynamic(mtpncGVR, peer.MTPNC)
	}
	for _, service := range input.Services {
		add(s.services.Informer().GetIndexer(), service.Service)
		for _, pod := range service.Pods {
			add(s.pods.Informer().GetIndexer(), pod)
		}
		for _, slice := range service.Slices {
			add(s.slices.Informer().GetIndexer(), slice)
		}
	}
	for _, worker := range input.Workers {
		addDynamic(machineGVR, worker.Machine)
		addDynamic(azureMachineGVR, worker.AzureMachine)
	}
	add(s.secrets.Informer().GetIndexer(), trustFixtures()[0])
	add(s.configMaps.Informer().GetIndexer(), trustFixtures()[1])
	return s
}

func TestGather(t *testing.T) {
	input := discoveryInput(t)
	s := cachedDiscoveryInput(t, input)
	// A different PNI and namespace must never require their MTPNCs.
	unrelated := routerPod("unrelated", "unrelated", "node", "10.0.0.99")
	unrelated.Labels[pniLabel] = "other"
	if err := s.pods.Informer().GetIndexer().Add(unrelated); err != nil {
		t.Fatal(err)
	}
	other := routerPod("other", "other", "node", "10.0.0.98")
	other.Namespace = "other"
	if err := s.pods.Informer().GetIndexer().Add(other); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(t.Context(), s, input.Pod, input.DNS)
	if err != nil {
		t.Fatal(err)
	}
	want, err := BuildDiscovery(input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("gather changed mapping: err=%v\ngot=%+v\nwant=%+v", err, got, want)
	}
	if len(s.HasSynced()) != 9 {
		t.Fatalf("not all discovery informers included: %d", len(s.HasSynced()))
	}
}

func TestGatherMissingResources(t *testing.T) {
	for _, name := range []string{"route", "KAS route", "local MTPNC", "deleting peer MTPNC", "AzureMachine", "secret", "configmap", "service"} {
		t.Run(name, func(t *testing.T) {
			input := discoveryInput(t)
			s := cachedDiscoveryInput(t, input)
			var indexer cache.Indexer
			var key string
			switch name {
			case "route":
				indexer, key = s.objects[routeGVR].Informer().GetIndexer(), "ns/ignition-server"
			case "KAS route":
				indexer, key = s.objects[routeGVR].Informer().GetIndexer(), "ns/kube-apiserver-internal"
			case "local MTPNC":
				indexer, key = s.objects[mtpncGVR].Informer().GetIndexer(), "ns/router"
			case "deleting peer MTPNC":
				indexer, key = s.objects[mtpncGVR].Informer().GetIndexer(), "ns/peer"
			case "AzureMachine":
				indexer, key = s.objects[azureMachineGVR].Informer().GetIndexer(), "ns/worker-azure"
			case "secret":
				indexer, key = s.secrets.Informer().GetIndexer(), "ns/"+ignitionCAName
			case "configmap":
				indexer, key = s.configMaps.Informer().GetIndexer(), "ns/"+rootCAName
			case "service":
				indexer, key = s.services.Informer().GetIndexer(), "ns/ignition-server"
			}
			object, exists, err := indexer.GetByKey(key)
			if err != nil || !exists {
				t.Fatalf("fixture missing %s: %v", key, err)
			}
			if err := indexer.Delete(object); err != nil {
				t.Fatal(err)
			}
			d, err := Discover(t.Context(), s, input.Pod, input.DNS)
			if !apierrors.IsNotFound(err) || !reflect.DeepEqual(d, Discovery{}) {
				t.Fatalf("missing lookup must abort with NotFound: %+v %v", d, err)
			}
		})
	}
}

func TestDiscoveryInformerFilters(t *testing.T) {
	client := fake.NewClientset(trustFixtures()...)
	dynamicClient := discoveryClient()
	s := testDiscoverySources(t, client, dynamicClient)
	for _, check := range s.HasSynced() {
		if !check() {
			t.Fatal("startup did not sync every informer")
		}
	}
	expectedLabels := map[string]string{
		"pods":           "app in (private-router,ignition-server,ignition-server-proxy,kube-apiserver)",
		"endpointslices": discoveryv1.LabelServiceName + " in (ignition-server,ignition-server-proxy,kube-apiserver)",
		"routes":         "hypershift.openshift.io/hosted-control-plane",
		"machines":       clusterNameLabel,
		"azuremachines":  clusterNameLabel,
		"services":       "", "multitenantpodnetworkconfigs": "", "secrets": "", "configmaps": "",
	}
	seen := map[string]bool{}
	for _, action := range append(client.Actions(), dynamicClient.Actions()...) {
		resource := action.GetResource().Resource
		wantLabels, exists := expectedLabels[resource]
		if !exists {
			t.Fatalf("unexpected resource: %s", resource)
		}
		parsedLabels, err := labels.Parse(wantLabels)
		if err != nil {
			t.Fatal(err)
		}
		wantFields := ""
		if name := map[string]string{"secrets": ignitionCAName, "configmaps": rootCAName}[resource]; name != "" {
			wantFields = "metadata.name=" + name
		}
		if action.GetNamespace() != "" {
			t.Fatalf("%s watch is not cluster-wide", resource)
		}
		var labelSelector, fieldSelector string
		switch action := action.(type) {
		case clienttesting.ListAction:
			labelSelector, fieldSelector = action.GetListRestrictions().Labels.String(), action.GetListRestrictions().Fields.String()
		case clienttesting.WatchAction:
			labelSelector, fieldSelector = action.GetWatchRestrictions().Labels.String(), action.GetWatchRestrictions().Fields.String()
		default:
			t.Fatalf("unexpected discovery API read: %v", action)
		}
		if labelSelector != parsedLabels.String() || fieldSelector != wantFields {
			t.Fatalf("%s filters: labels=%q fields=%q; want labels=%q fields=%q", resource, labelSelector, fieldSelector, parsedLabels.String(), wantFields)
		}
		seen[resource] = true
	}
	if len(seen) != len(expectedLabels) {
		t.Fatalf("missing resource watches: %v", seen)
	}
}

func TestGatherRouteAlternatives(t *testing.T) {
	for _, name := range []string{"kube-apiserver-internal", "kube-apiserver", "kube-apiserver-private"} {
		t.Run(name, func(t *testing.T) {
			input := discoveryInput(t)
			input.KASRoutes[0].Name = name
			if name != "kube-apiserver-internal" {
				input.KASRoutes[0].Spec.Host = "customer.example.com"
			}
			if _, err := Discover(t.Context(), cachedDiscoveryInput(t, input), input.Pod, input.DNS); err != nil {
				t.Fatalf("missing optional alternatives must not abort: %v", err)
			}
		})
	}
}

func TestGatherDecodeFailure(t *testing.T) {
	input := discoveryInput(t)
	s := cachedDiscoveryInput(t, input)
	invalid := mtpnc(input.Pod, "10.1.0.1")
	if err := unstructured.SetNestedField(invalid.Object, int64(42), "spec", "podUID"); err != nil {
		t.Fatal(err)
	}
	if err := s.objects[mtpncGVR].Informer().GetIndexer().Update(invalid); err != nil {
		t.Fatal(err)
	}
	d, err := Discover(t.Context(), s, input.Pod, input.DNS)
	if err == nil || !reflect.DeepEqual(d, Discovery{}) {
		t.Fatalf("invalid wire data must abort: %+v %v", d, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Gather(ctx, s, input.Pod, input.DNS); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
}

func TestBuildDiscoveryAllPeers(t *testing.T) {
	input := discoveryInput(t)
	input.Peers = nil
	for i := range 140 {
		peer := routerPod(fmt.Sprintf("peer-%d", i), fmt.Sprintf("uid-%d", i), "node", fmt.Sprintf("10.0.1.%d", i+1))
		input.Peers = append(input.Peers, RouterInput{Pod: peer, MTPNC: decodedMTPNC(t, peer, fmt.Sprintf("10.1.1.%d", i+1))})
	}
	d, err := BuildDiscovery(input)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, info := range d.Targets {
		counts[info.Target.Role]++
	}
	for _, role := range []string{"peer-management", "peer-swift", "kas-peer-management", "kas-peer-swift"} {
		if counts[role] != 140 {
			t.Fatalf("peers capped: %v", counts)
		}
	}
}
