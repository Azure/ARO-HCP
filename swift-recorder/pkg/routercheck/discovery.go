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
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

const pniLabel = "kubernetes.azure.com/pod-network-instance"
const swiftResource corev1.ResourceName = "aro.openshift.io/swift-nic"

var mtpncGVR = schema.GroupVersionResource{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "multitenantpodnetworkconfigs"}
var routeGVR = schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}

// ErrDiscoveryData identifies missing or inconsistent resource data.
var ErrDiscoveryData = errors.New("discovery data unavailable or inconsistent")

type TargetInfo struct {
	Target            probe.Target            `json:"target"`
	PodName           string                  `json:"pod_name,omitempty"`
	PodUID            string                  `json:"pod_uid,omitempty"`
	Node              string                  `json:"node,omitempty"`
	Ready             *bool                   `json:"ready,omitempty"`
	Deleting          bool                    `json:"deleting"`
	Slice             string                  `json:"endpoint_slice,omitempty"`
	Ref               *corev1.ObjectReference `json:"target_ref,omitempty"`
	SelectorMatches   *bool                   `json:"selector_matches,omitempty"`
	TargetPortMatches *bool                   `json:"target_port_matches,omitempty"`
	Workers           []WorkerInfo            `json:"workers,omitempty"`
}

type SwiftInterface struct {
	IP     string `json:"ip"`
	CIDR   string `json:"cidr,omitempty"`
	MAC    string `json:"mac,omitempty"`
	Source string `json:"source"`
}

type Discovery struct {
	Targets      []TargetInfo
	Swift        []SwiftInterface
	DNSNames     []string
	TrustBundles map[string]string `json:"-"`
}

// MTPNC is the subset of the Azure CNI CRD needed for discovery.
type MTPNC struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		PodName            string `json:"podName,omitempty"`
		PodUID             string `json:"podUID,omitempty"`
		PodNetworkInstance string `json:"podNetworkInstance,omitempty"`
		NodeName           string `json:"nodeName,omitempty"`
	} `json:"spec,omitempty"`
	Status struct {
		NodeName       string           `json:"nodeName,omitempty"`
		PrimaryIP      string           `json:"primaryIP,omitempty"`
		MACAddress     string           `json:"macAddress,omitempty"`
		InterfaceInfos []MTPNCInterface `json:"interfaceInfos,omitempty"`
	} `json:"status,omitempty"`
}

type MTPNCInterface struct {
	DeviceType string `json:"deviceType,omitempty"`
	PrimaryIP  string `json:"primaryIP,omitempty"`
	MACAddress string `json:"macAddress,omitempty"`
}

// DiscoveryInput is a read-only snapshot of the resources used by one pass.
// BuildDiscovery never modifies or retains mutable aliases to these resources.
type DiscoveryInput struct {
	Pod           *corev1.Pod
	DNS           probe.DNSConfig
	IgnitionRoute *routev1.Route
	KASRoutes     []*routev1.Route
	LocalMTPNC    *MTPNC
	Peers         []RouterInput
	Services      []ServiceInput
	Workers       []WorkerInput
	TrustBundles  map[string]string
}

type RouterInput struct {
	Pod   *corev1.Pod
	MTPNC *MTPNC
}

type ServiceInput struct {
	Service *corev1.Service
	Pods    []*corev1.Pod
	Slices  []*discoveryv1.EndpointSlice
}

func swiftRouter(pod *corev1.Pod) bool {
	if pod.Labels["app"] != "private-router" || pod.Labels[pniLabel] == "" || pod.Spec.HostNetwork || pod.UID == "" {
		return false
	}
	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, c := range containers {
			for _, resources := range []corev1.ResourceList{c.Resources.Requests, c.Resources.Limits} {
				if q := resources[swiftResource]; q.Sign() > 0 {
					return true
				}
			}
		}
	}
	return false
}

func podReady(pod *corev1.Pod) *bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			b := c.Status == corev1.ConditionTrue
			return &b
		}
	}
	return nil
}

// Discover gathers cached resources and builds an all-or-nothing probe plan.
func Discover(ctx context.Context, sources *discoverySources, pod *corev1.Pod, dns probe.DNSConfig) (Discovery, error) {
	input, err := Gather(ctx, sources, pod, dns)
	if err != nil {
		return Discovery{}, err
	}
	return BuildDiscovery(input)
}

// BuildDiscovery is a pure transformation that requires complete input.
func BuildDiscovery(input DiscoveryInput) (Discovery, error) {
	if input.Pod == nil || !swiftRouter(input.Pod) {
		return Discovery{}, fmt.Errorf("router identity: %w", ErrDiscoveryData)
	}
	for _, id := range []string{probe.TrustIgnition, probe.TrustRoot} {
		if bundle := input.TrustBundles[id]; len(bundle) == 0 || len(bundle) > maxTrustBundleBytes {
			return Discovery{}, fmt.Errorf("trust bundle %s: %w", id, ErrDiscoveryData)
		}
	}
	if input.IgnitionRoute == nil || input.IgnitionRoute.Spec.Host == "" {
		return Discovery{}, fmt.Errorf("ignition route host: %w", ErrDiscoveryData)
	}
	var kasService *corev1.Service
	for _, service := range input.Services {
		if service.Service != nil && service.Service.Name == "kube-apiserver" {
			kasService = service.Service
		}
	}
	if kasService == nil {
		return Discovery{}, fmt.Errorf("kube-apiserver service: %w", ErrDiscoveryData)
	}
	owner, err := hostedControlPlaneOwner(kasService)
	if err != nil || owner == nil || kasService.Namespace != input.Pod.Namespace {
		return Discovery{}, fmt.Errorf("KAS service owner: %w", ErrDiscoveryData)
	}
	ignitionOwner, err := hostedControlPlaneOwner(input.IgnitionRoute)
	if err != nil || (ignitionOwner != nil && (ignitionOwner.Name != owner.Name || ignitionOwner.UID != owner.UID)) {
		return Discovery{}, fmt.Errorf("ignition route owner: %w", ErrDiscoveryData)
	}
	kasHost := "api." + owner.Name + ".hypershift.local"
	if len(input.KASRoutes) == 0 {
		return Discovery{}, fmt.Errorf("KAS routes: %w", ErrDiscoveryData)
	}
	for _, route := range input.KASRoutes {
		if route == nil {
			return Discovery{}, fmt.Errorf("KAS route: %w", ErrDiscoveryData)
		}
		routeOwner, err := hostedControlPlaneOwner(route)
		if err != nil || routeOwner == nil || routeOwner.Name != owner.Name || routeOwner.UID != owner.UID || route.Namespace != input.Pod.Namespace || route.Labels["hypershift.openshift.io/hosted-control-plane"] != input.Pod.Namespace || route.Spec.To.Name != kasService.Name || (route.Spec.To.Kind != "" && route.Spec.To.Kind != "Service") || route.Spec.TLS == nil || route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough || route.Spec.Host == "" || (route.Name == "kube-apiserver-internal" && route.Spec.Host != kasHost) {
			return Discovery{}, fmt.Errorf("KAS route %s identity: %w", route.Name, ErrDiscoveryData)
		}
		// External routes establish availability, never certificate identity.
	}
	swift, err := swiftInterfaces(input.LocalMTPNC, input.Pod)
	if err != nil {
		return Discovery{}, err
	}
	routers, err := routerTargets(input, swift, kasHost)
	if err != nil {
		return Discovery{}, err
	}
	var services []TargetInfo
	var dnsNames []string
	var domain string
	for _, search := range input.DNS.Searches {
		search = strings.TrimSuffix(search, ".")
		if strings.HasPrefix(search, "svc.") {
			domain = strings.TrimPrefix(search, "svc.")
			break
		}
		if _, suffix, ok := strings.Cut(search, ".svc."); ok {
			domain = suffix
			break
		}
	}
	if domain == "" || len(input.DNS.Servers) == 0 {
		return Discovery{}, fmt.Errorf("sandbox DNS configuration: %w", ErrDiscoveryData)
	}
	for _, name := range []string{"ignition-server", "ignition-server-proxy", "kube-apiserver"} {
		var service *ServiceInput
		for i := range input.Services {
			if input.Services[i].Service != nil && input.Services[i].Service.Name == name {
				service = &input.Services[i]
				break
			}
		}
		if service == nil {
			return Discovery{}, fmt.Errorf("service %s: %w", name, ErrDiscoveryData)
		}
		targets, err := serviceTargets(*service, input.IgnitionRoute.Spec.Host, kasHost)
		if err != nil {
			return Discovery{}, err
		}
		services = append(services, targets...)
		dnsNames = append(dnsNames, name+"."+input.Pod.Namespace+".svc."+domain+".")
	}
	workers, err := workerTargets(input.Workers, swift)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{
		Targets: append(append(routers, services...), workers...),
		Swift:   swift, DNSNames: dnsNames, TrustBundles: maps.Clone(input.TrustBundles),
	}, nil
}

func newTarget(role, address string, port int, source, serverName, trustID string, pod *corev1.Pod) (TargetInfo, error) {
	if _, err := netip.ParseAddr(address); err != nil || port <= 0 || port > 65535 || (serverName == "" && role != "haproxy-ready") {
		return TargetInfo{}, fmt.Errorf("target %s address, port or SNI: %w", role, ErrDiscoveryData)
	}
	target := probe.Target{ID: fmt.Sprintf("%s/%s/%d/%s", role, address, port, source), Role: role, Address: address, Port: port, Path: "/healthz", SourceIP: source, ServerName: serverName, TrustBundle: trustID}
	if strings.HasPrefix(role, "kas-") {
		target.Path = "/readyz"
		target.LivenessOnFailure = true
	}
	if role == "haproxy-ready" {
		target.ServerName, target.TrustBundle = "", ""
		target.PlainHTTP = true
		target.Path = "/haproxy_ready"
	}
	info := TargetInfo{Target: target}
	if pod != nil {
		info.PodName, info.PodUID, info.Node = pod.Name, string(pod.UID), pod.Spec.NodeName
		info.Ready, info.Deleting = podReady(pod), pod.DeletionTimestamp != nil
	}
	return info, nil
}

func routerTargets(input DiscoveryInput, localSwift []SwiftInterface, kasHost string) ([]TargetInfo, error) {
	ready, err := newTarget("haproxy-ready", "127.0.0.1", 9444, "", "", "", input.Pod)
	if err != nil {
		return nil, err
	}
	targets := []TargetInfo{ready}
	type address struct {
		role, ip, source string
		pod              *corev1.Pod
	}
	addresses := []address{{role: "router-loopback", ip: "127.0.0.1", pod: input.Pod}}
	if len(podIPs(input.Pod)) == 0 {
		return nil, fmt.Errorf("router management IP: %w", ErrDiscoveryData)
	}
	for _, ip := range podIPs(input.Pod) {
		addresses = append(addresses, address{role: "router-management", ip: ip, pod: input.Pod})
	}
	for _, iface := range localSwift {
		addresses = append(addresses, address{role: "router-swift", ip: iface.IP, pod: input.Pod})
	}
	for _, peer := range input.Peers {
		if len(podIPs(peer.Pod)) == 0 {
			return nil, fmt.Errorf("peer %s management IP: %w", peer.Pod.Name, ErrDiscoveryData)
		}
		for _, ip := range podIPs(peer.Pod) {
			addresses = append(addresses, address{role: "peer-management", ip: ip, pod: peer.Pod})
		}
		interfaces, err := swiftInterfaces(peer.MTPNC, peer.Pod)
		if err != nil {
			return nil, err
		}
		for _, iface := range interfaces {
			bound := false
			for _, source := range localSwift {
				if sameFamily(source.IP, iface.IP) {
					addresses = append(addresses, address{role: "peer-swift", ip: iface.IP, source: source.IP, pod: peer.Pod})
					bound = true
				}
			}
			if !bound {
				return nil, fmt.Errorf("peer %s Swift source family: %w", peer.Pod.Name, ErrDiscoveryData)
			}
		}
	}
	for _, address := range addresses {
		ignition, err := newTarget(address.role, address.ip, 8443, address.source, input.IgnitionRoute.Spec.Host, probe.TrustIgnition, address.pod)
		if err != nil {
			return nil, err
		}
		kas, err := newTarget("kas-"+address.role, address.ip, 8443, address.source, kasHost, probe.TrustRoot, address.pod)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ignition, kas)
	}
	return targets, nil
}

func serviceTargets(input ServiceInput, ignitionHost, kasHost string) ([]TargetInfo, error) {
	svc := input.Service
	if len(input.Pods) == 0 || len(input.Slices) == 0 || len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("service %s pods, selector or endpoints: %w", svc.Name, ErrDiscoveryData)
	}
	role, serverName, trustID := svc.Name, ignitionHost, probe.TrustIgnition
	switch svc.Name {
	case "ignition-server":
		serverName, trustID = "ignition-server."+svc.Namespace+".svc", probe.TrustRoot
	case "kube-apiserver":
		role, serverName, trustID = "kas", kasHost, probe.TrustRoot
	}
	var targets []TargetInfo
	for _, port := range svc.Spec.Ports {
		if port.Protocol != "" && port.Protocol != corev1.ProtocolTCP {
			continue
		}
		if role == "kas" {
			if port.Name != "client" && port.TargetPort != intstr.FromString("client") {
				continue
			}
		} else if port.Name != "https" && len(svc.Spec.Ports) != 1 {
			continue
		}
		ips := svc.Spec.ClusterIPs
		if len(ips) == 0 && svc.Spec.ClusterIP != "" {
			ips = []string{svc.Spec.ClusterIP}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("service %s ClusterIP: %w", svc.Name, ErrDiscoveryData)
		}
		for _, ip := range ips {
			target, err := newTarget(role+"-service", ip, int(port.Port), "", serverName, trustID, nil)
			if err != nil {
				return nil, err
			}
			targets = append(targets, target)
		}
		for _, slice := range input.Slices {
			matched := false
			for _, epPort := range slice.Ports {
				if epPort.Port == nil || *epPort.Port <= 0 || (epPort.Protocol != nil && *epPort.Protocol != corev1.ProtocolTCP) {
					continue
				}
				portName := ""
				if epPort.Name != nil {
					portName = *epPort.Name
				}
				if portName != port.Name || (port.TargetPort.Type == intstr.Int && port.TargetPort.IntVal != 0 && port.TargetPort.IntVal != *epPort.Port) {
					continue
				}
				matched = true
				if len(slice.Endpoints) == 0 {
					return nil, fmt.Errorf("EndpointSlice %s endpoints: %w", slice.Name, ErrDiscoveryData)
				}
				for _, endpoint := range slice.Endpoints {
					if len(endpoint.Addresses) == 0 {
						return nil, fmt.Errorf("EndpointSlice %s addresses: %w", slice.Name, ErrDiscoveryData)
					}
					for _, address := range endpoint.Addresses {
						info, err := newTarget(role+"-endpoint", address, int(*epPort.Port), "", serverName, trustID, nil)
						if err != nil {
							return nil, err
						}
						if endpoint.Conditions.Ready != nil {
							ready := *endpoint.Conditions.Ready
							info.Ready = &ready
						}
						info.Ref, info.Slice = endpoint.TargetRef.DeepCopy(), slice.Name
						if endpoint.NodeName != nil {
							info.Node = *endpoint.NodeName
						}
						info.Deleting = endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating
						if ref := endpoint.TargetRef; ref != nil && ref.Kind == "Pod" {
							info.PodName, info.PodUID = ref.Name, string(ref.UID)
							matched := false
							info.SelectorMatches = &matched
							for _, pod := range input.Pods {
								if pod.Name != ref.Name || (ref.UID != "" && pod.UID != ref.UID) || (ref.Namespace != "" && ref.Namespace != pod.Namespace) {
									continue
								}
								matched, info.Node = true, pod.Spec.NodeName
								if port.TargetPort.Type == intstr.String {
									portMatches := false
									info.TargetPortMatches = &portMatches
									for _, container := range pod.Spec.Containers {
										for _, cp := range container.Ports {
											if cp.Name == port.TargetPort.StrVal && cp.ContainerPort == *epPort.Port && (cp.Protocol == "" || cp.Protocol == corev1.ProtocolTCP) {
												portMatches = true
											}
										}
									}
								}
							}
							if !matched {
								return nil, fmt.Errorf("EndpointSlice %s target Pod %s: %w", slice.Name, ref.Name, ErrDiscoveryData)
							}
						}
						targets = append(targets, info)
					}
				}
			}
			if !matched {
				return nil, fmt.Errorf("EndpointSlice %s serving port: %w", slice.Name, ErrDiscoveryData)
			}
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("service %s serving port: %w", svc.Name, ErrDiscoveryData)
	}
	return targets, nil
}

func hostedControlPlaneOwner(object metav1.Object) (*metav1.OwnerReference, error) {
	var owner *metav1.OwnerReference
	for _, ref := range object.GetOwnerReferences() {
		if ref.Kind != "HostedControlPlane" {
			continue
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil || gv.Group != "hypershift.openshift.io" || ref.Name == "" || ref.UID == "" || owner != nil {
			return nil, fmt.Errorf("invalid HCP owner: %w", ErrDiscoveryData)
		}
		owner = &ref
	}
	return owner, nil
}

func podIPs(pod *corev1.Pod) []string {
	var ips []string
	for _, ip := range pod.Status.PodIPs {
		ips = append(ips, ip.IP)
	}
	if len(ips) == 0 && pod.Status.PodIP != "" {
		ips = append(ips, pod.Status.PodIP)
	}
	return ips
}

func sameFamily(a, b string) bool {
	x, e1 := netip.ParseAddr(a)
	y, e2 := netip.ParseAddr(b)
	return e1 == nil && e2 == nil && x.Is4() == y.Is4()
}

func swiftInterfaces(mtpnc *MTPNC, pod *corev1.Pod) ([]SwiftInterface, error) {
	if mtpnc == nil || mtpnc.Spec.PodName != pod.Name || mtpnc.Spec.PodUID != string(pod.UID) || mtpnc.Spec.PodUID == "" {
		return nil, fmt.Errorf("MTPNC %s Pod identity: %w", pod.Name, ErrDiscoveryData)
	}
	if mtpnc.Spec.PodNetworkInstance != "" && mtpnc.Spec.PodNetworkInstance != pod.Labels[pniLabel] {
		return nil, fmt.Errorf("MTPNC %s network instance: %w", pod.Name, ErrDiscoveryData)
	}
	for _, ref := range mtpnc.OwnerReferences {
		if ref.Kind == "Pod" && (ref.UID != pod.UID || ref.Name != pod.Name) {
			return nil, fmt.Errorf("MTPNC %s owner: %w", pod.Name, ErrDiscoveryData)
		}
	}
	for _, node := range []string{mtpnc.Spec.NodeName, mtpnc.Status.NodeName} {
		if node != "" && node != pod.Spec.NodeName {
			return nil, fmt.Errorf("MTPNC %s node: %w", pod.Name, ErrDiscoveryData)
		}
	}
	interfaces, source := mtpnc.Status.InterfaceInfos, "interfaceInfos"
	if interfaces == nil {
		// Shipped CNS versions predate interfaceInfos and publish primaryIP only.
		interfaces = []MTPNCInterface{{DeviceType: "acn.azure.com/vnet-nic", PrimaryIP: mtpnc.Status.PrimaryIP, MACAddress: mtpnc.Status.MACAddress}}
		source = "deprecated_primaryIP"
	}
	var result []SwiftInterface
	for _, iface := range interfaces {
		if iface.DeviceType != "acn.azure.com/vnet-nic" {
			continue
		}
		ip, err := netip.ParseAddr(iface.PrimaryIP)
		cidr := ""
		if err != nil {
			prefix, err := netip.ParsePrefix(iface.PrimaryIP)
			if err != nil {
				return nil, fmt.Errorf("MTPNC %s address: %w", pod.Name, ErrDiscoveryData)
			}
			ip, cidr = prefix.Addr(), iface.PrimaryIP
		}
		if !ip.IsGlobalUnicast() || ip.Zone() != "" {
			return nil, fmt.Errorf("MTPNC %s address: %w", pod.Name, ErrDiscoveryData)
		}
		if iface.MACAddress != "" {
			if _, err := net.ParseMAC(iface.MACAddress); err != nil {
				return nil, fmt.Errorf("MTPNC %s MAC: %w", pod.Name, ErrDiscoveryData)
			}
		}
		result = append(result, SwiftInterface{IP: ip.Unmap().String(), CIDR: cidr, MAC: iface.MACAddress, Source: source})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("MTPNC %s interfaces: %w", pod.Name, ErrDiscoveryData)
	}
	return result, nil
}
