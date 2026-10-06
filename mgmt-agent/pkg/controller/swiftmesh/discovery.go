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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

// MultusNetworkStatusAnnotation is the de-facto standard annotation Multus
// writes on a pod, listing every attached network and its assigned IP(s). The
// SWIFT NIC IP is NOT pod.Status.PodIP (that is the primary CNI interface); it
// is the IP of the SwiftV2 attachment, read from this annotation.
const MultusNetworkStatusAnnotation = "k8s.v1.cni.cncf.io/networks-status"

// multusNetworkStatus is the subset of a networks-status entry we consume.
type multusNetworkStatus struct {
	Name      string   `json:"name"`
	Interface string   `json:"interface"`
	IPs       []string `json:"ips"`
}

// Discoverer resolves the per-HCP router meshes to probe in one sweep.
type Discoverer interface {
	Discover(ctx context.Context) ([]HCPMesh, error)
}

// HCPDiscoverer enumerates router pods across all namespaces from a cluster-wide
// informer-backed lister and groups them into one mesh per HCP control-plane
// namespace. Because it reads from a shared informer, router pods of newly
// deployed HCPs appear automatically on the next sweep, and pods of deleted HCPs
// drop out.
type HCPDiscoverer struct {
	podLister       corelisters.PodLister
	namespaceLister corelisters.NamespaceLister
	labelSelector   labels.Selector
	container       string
	// networkName identifies the SwiftV2 attachment within the networks-status
	// annotation. Matched against each entry's Name, either exactly or as the
	// "<namespace>/<name>" suffix.
	networkName string
	port        int
}

// NewHCPDiscoverer builds an HCPDiscoverer. networkName must be non-empty: it is
// the only reliable way to pick the SWIFT NIC out of a pod's attachments.
func NewHCPDiscoverer(podLister corelisters.PodLister, namespaceLister corelisters.NamespaceLister, labelSelector labels.Selector, container, networkName string, port int) *HCPDiscoverer {
	return &HCPDiscoverer{
		podLister:       podLister,
		namespaceLister: namespaceLister,
		labelSelector:   labelSelector,
		container:       container,
		networkName:     networkName,
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
		ip, ok := swiftIPFromPod(pod, d.networkName)
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

// swiftIPFromPod returns the first IP of the attachment matching networkName.
func swiftIPFromPod(pod *corev1.Pod, networkName string) (string, bool) {
	raw, ok := pod.Annotations[MultusNetworkStatusAnnotation]
	if !ok || raw == "" {
		return "", false
	}
	var statuses []multusNetworkStatus
	if err := json.Unmarshal([]byte(raw), &statuses); err != nil {
		return "", false
	}
	for _, s := range statuses {
		if !networkMatches(s.Name, networkName) {
			continue
		}
		for _, ip := range s.IPs {
			if ip != "" {
				return ip, true
			}
		}
	}
	return "", false
}

// networkMatches accepts an exact match or a "<namespace>/<name>" suffix match,
// since Multus reports attachment names with or without a namespace prefix.
func networkMatches(statusName, networkName string) bool {
	return statusName == networkName || strings.HasSuffix(statusName, "/"+networkName)
}
