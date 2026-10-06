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
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamiclister"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

// MTPNCGroupVersionResource is Azure CNS's MultitenantPodNetworkConfig. One
// exists per SwiftV2 pod, named after the pod, in the pod's namespace, and its
// status carries the allocated SWIFT NIC IP. This is the authoritative source;
// the SWIFT NIC IP is NOT on pod.Status.PodIP nor in a Multus annotation.
var MTPNCGroupVersionResource = schema.GroupVersionResource{
	Group:    "multitenancy.acn.azure.com",
	Version:  "v1alpha1",
	Resource: "multitenantpodnetworkconfigs",
}

// vnetNICDeviceType is the MTPNC InterfaceInfo DeviceType for the vnet (data
// path) NIC, as opposed to acn.azure.com/infiniband-nic.
const vnetNICDeviceType = "acn.azure.com/vnet-nic"

// Discoverer resolves the per-HCP router meshes to probe in one sweep.
type Discoverer interface {
	Discover(ctx context.Context) ([]HCPMesh, error)
}

// HCPDiscoverer enumerates router pods across all namespaces from a cluster-wide
// informer-backed lister and groups them into one mesh per HCP control-plane
// namespace. Each pod's SWIFT NIC IP is resolved from its MTPNC. Because both
// listers are informer-backed, router pods of newly deployed HCPs appear
// automatically on the next sweep, and pods of deleted HCPs drop out.
type HCPDiscoverer struct {
	podLister       corelisters.PodLister
	namespaceLister corelisters.NamespaceLister
	mtpncLister     dynamiclister.Lister
	labelSelector   labels.Selector
	container       string
	port            int
}

// NewHCPDiscoverer builds an HCPDiscoverer.
func NewHCPDiscoverer(podLister corelisters.PodLister, namespaceLister corelisters.NamespaceLister, mtpncLister dynamiclister.Lister, labelSelector labels.Selector, container string, port int) *HCPDiscoverer {
	return &HCPDiscoverer{
		podLister:       podLister,
		namespaceLister: namespaceLister,
		mtpncLister:     mtpncLister,
		labelSelector:   labelSelector,
		container:       container,
		port:            port,
	}
}

func (d *HCPDiscoverer) Discover(_ context.Context) ([]HCPMesh, error) {
	pods, err := d.podLister.List(d.labelSelector)
	if err != nil {
		return nil, fmt.Errorf("list router pods: %w", err)
	}

	// Group running router pods with a resolvable SWIFT NIC IP by namespace.
	byNamespace := map[string][]RouterPod{}
	for _, pod := range pods {
		// Only running pods can carry traffic or be exec'd.
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		ip, ok := d.swiftIP(pod.Namespace, pod.Name)
		if !ok {
			continue
		}
		byNamespace[pod.Namespace] = append(byNamespace[pod.Namespace], RouterPod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Container: d.container,
			SwiftIP:   ip,
		})
	}

	meshes := make([]HCPMesh, 0, len(byNamespace))
	for namespace, routers := range byNamespace {
		// Only probe namespaces that are HCP control planes, identified by the
		// Azure resource ID annotation the platform stamps on them. This both
		// scopes the probe to HCPs and yields the correlation ID for metrics.
		resourceID, ok := d.hcpResourceID(namespace)
		if !ok {
			continue
		}
		// Deterministic order keeps edge enumeration and logs stable.
		sort.Slice(routers, func(i, j int) bool { return routers[i].Name < routers[j].Name })
		meshes = append(meshes, HCPMesh{
			Namespace:  namespace,
			ResourceID: resourceID,
			Routers:    routers,
			Port:       d.port,
		})
	}
	sort.Slice(meshes, func(i, j int) bool { return meshes[i].Namespace < meshes[j].Namespace })
	return meshes, nil
}

// swiftIP resolves a router pod's SWIFT NIC IP from its MTPNC (same name and
// namespace as the pod). Returns false when the MTPNC is absent or has no IP yet.
func (d *HCPDiscoverer) swiftIP(namespace, podName string) (string, bool) {
	obj, err := d.mtpncLister.Namespace(namespace).Get(podName)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			// A non-NotFound error is unexpected; treat the pod as not-yet-ready.
			return "", false
		}
		return "", false
	}
	return swiftIPFromMTPNC(obj)
}

// hcpResourceID returns the HCP Azure resource ID annotation on the namespace,
// and false when the namespace is absent or not an HCP control-plane namespace.
func (d *HCPDiscoverer) hcpResourceID(namespace string) (string, bool) {
	ns, err := d.namespaceLister.Get(namespace)
	if err != nil {
		return "", false
	}
	id := ns.Annotations[controllerutils.HcpClusterAzureResourceIdAnnotation]
	if id == "" {
		return "", false
	}
	return id, true
}

// swiftIPFromMTPNC extracts the SWIFT NIC IP from an MTPNC's status. It prefers
// the vnet (data-path) NIC's primaryIP, falls back to any interface's primaryIP,
// then to the deprecated top-level status.primaryIP.
func swiftIPFromMTPNC(u *unstructured.Unstructured) (string, bool) {
	infos, found, err := unstructured.NestedSlice(u.Object, "status", "interfaceInfos")
	if err == nil && found {
		if ip, ok := pickInterfaceIP(infos, vnetNICDeviceType); ok {
			return ip, true
		}
		if ip, ok := pickInterfaceIP(infos, ""); ok {
			return ip, true
		}
	}
	// Deprecated single-NIC fields, for older CNS versions.
	if ip, found, err := unstructured.NestedString(u.Object, "status", "primaryIP"); err == nil && found {
		if ip = trimMask(ip); ip != "" {
			return ip, true
		}
	}
	return "", false
}

// pickInterfaceIP returns the first interfaceInfos entry's primaryIP. When
// deviceType is non-empty, only entries with that deviceType are considered.
func pickInterfaceIP(infos []any, deviceType string) (string, bool) {
	for _, raw := range infos {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if deviceType != "" {
			dt, _, _ := unstructured.NestedString(entry, "deviceType")
			if dt != deviceType {
				continue
			}
		}
		ip, _, _ := unstructured.NestedString(entry, "primaryIP")
		if ip = trimMask(ip); ip != "" {
			return ip, true
		}
	}
	return "", false
}

// trimMask strips a trailing CIDR mask ("10.0.0.5/32" -> "10.0.0.5"); MTPNC may
// report either form.
func trimMask(ip string) string {
	bare, _, _ := strings.Cut(ip, "/")
	return bare
}
