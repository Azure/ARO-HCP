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

// Package routercheck continuously checks the node-local Swift private routers.
package routercheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	corev1 "k8s.io/api/core/v1"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

const apiTimeout = 5 * time.Second

// Sandbox contains only the runtime fields needed by the probe subprocess.
type Sandbox struct {
	ID            string
	Path          string
	Device, Inode uint64
	DNS           probe.DNSConfig
}

// RuntimeFailure identifies the runtime data required to execute a probe pass.
type RuntimeFailure string

const (
	SandboxListUnavailable              RuntimeFailure = "sandbox_list_unavailable"
	SandboxInfoInvalid                  RuntimeFailure = "sandbox_info_invalid"
	SandboxAmbiguous                    RuntimeFailure = "sandbox_ambiguous"
	SandboxNotFound                     RuntimeFailure = "sandbox_not_found"
	SandboxStatusUnavailable            RuntimeFailure = "sandbox_status_unavailable"
	SandboxIdentityChanged              RuntimeFailure = "sandbox_identity_changed"
	SandboxNamespaceClosed              RuntimeFailure = "sandbox_namespace_closed"
	SandboxNamespaceAmbiguous           RuntimeFailure = "sandbox_namespace_ambiguous"
	SandboxNamespaceUnavailable         RuntimeFailure = "sandbox_namespace_unavailable"
	SandboxNamespaceIdentityUnavailable RuntimeFailure = "sandbox_namespace_identity_unavailable"
	SandboxNamespaceInvalid             RuntimeFailure = "sandbox_namespace_invalid"
	SandboxNamespaceOutsideDirectory    RuntimeFailure = "sandbox_namespace_outside_directory"
	SandboxNamespaceParentInvalid       RuntimeFailure = "sandbox_namespace_parent_invalid"
	SandboxDNSServersUnavailable        RuntimeFailure = "sandbox_dns_servers_unavailable"
	SandboxResolverMountUnavailable     RuntimeFailure = "sandbox_resolver_mount_unavailable"
	RouterContainerIdentityUnavailable  RuntimeFailure = "router_container_identity_unavailable"
	RouterContainerStatusUnavailable    RuntimeFailure = "router_container_status_unavailable"
	RouterContainerIdentityMismatch     RuntimeFailure = "router_container_identity_mismatch"
	RouterContainerInfoUnavailable      RuntimeFailure = "router_container_info_unavailable"
	RouterContainerSandboxMismatch      RuntimeFailure = "router_container_sandbox_mismatch"
	RouterResolverMountUnavailable      RuntimeFailure = "router_resolver_mount_unavailable"
	RouterResolverOverride              RuntimeFailure = "router_resolver_override"
)

// RuntimeError exposes a stable diagnostic reason while retaining the cause for
// errors.Is and errors.As. Error omits potentially sensitive CRI response details.
type RuntimeError struct {
	Reason RuntimeFailure
	Err    error
}

func (e *RuntimeError) Error() string { return string(e.Reason) }
func (e *RuntimeError) Unwrap() error { return e.Err }

// Runtime resolves a unique, ready sandbox for the current pod identity.
type Runtime interface {
	Sandbox(context.Context, *corev1.Pod) (Sandbox, error)
}

// CRIReader supplies sandbox identity and resolver configuration.
type CRIReader interface {
	ListPodSandbox(context.Context, *runtimev1.ListPodSandboxRequest, ...grpc.CallOption) (*runtimev1.ListPodSandboxResponse, error)
	PodSandboxStatus(context.Context, *runtimev1.PodSandboxStatusRequest, ...grpc.CallOption) (*runtimev1.PodSandboxStatusResponse, error)
	ContainerStatus(context.Context, *runtimev1.ContainerStatusRequest, ...grpc.CallOption) (*runtimev1.ContainerStatusResponse, error)
}

type runtimeMount struct {
	Destination string `json:"destination"`
	Source      string `json:"source"`
}

func resolverSource(mounts []runtimeMount) string {
	var source string
	for _, mount := range mounts {
		if mount.Destination != "/etc/resolv.conf" {
			continue
		}
		if source != "" || !filepath.IsAbs(mount.Source) || filepath.Clean(mount.Source) != mount.Source {
			return ""
		}
		source = mount.Source
	}
	return source
}

type CRIRuntime struct {
	Client   CRIReader
	NetNSDir string
}

// NewRuntime creates a lazy Unix connection. The caller owns Close.
func NewRuntime(endpoint, netNSDir string) (*CRIRuntime, *grpc.ClientConn, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4*1024*1024)))
	if err != nil {
		return nil, nil, err
	}
	return &CRIRuntime{Client: runtimev1.NewRuntimeServiceClient(conn), NetNSDir: netNSDir}, conn, nil
}

func ValidateEndpoint(endpoint string) error {
	path, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "?#") || path == "/" {
		return fmt.Errorf("runtime-endpoint must be a clean absolute unix:/// socket path")
	}
	return nil
}

func (r *CRIRuntime) Sandbox(ctx context.Context, pod *corev1.Pod) (Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	list, err := r.Client.ListPodSandbox(ctx, &runtimev1.ListPodSandboxRequest{Filter: &runtimev1.PodSandboxFilter{
		State:         &runtimev1.PodSandboxStateValue{State: runtimev1.PodSandboxState_SANDBOX_READY},
		LabelSelector: map[string]string{"io.kubernetes.pod.uid": string(pod.UID)},
	}})
	if err != nil {
		return Sandbox{}, &RuntimeError{Reason: SandboxListUnavailable, Err: err}
	}
	var id string
	for _, item := range list.GetItems() {
		if item.GetState() != runtimev1.PodSandboxState_SANDBOX_READY || !matchesPod(item.GetMetadata(), pod) {
			continue
		}
		if item.GetId() == "" || len(item.GetId()) > 256 {
			return Sandbox{}, &RuntimeError{Reason: SandboxInfoInvalid}
		}
		if id != "" {
			return Sandbox{}, &RuntimeError{Reason: SandboxAmbiguous}
		}
		id = item.GetId()
	}
	if id == "" {
		return Sandbox{}, &RuntimeError{Reason: SandboxNotFound}
	}
	response, err := r.Client.PodSandboxStatus(ctx, &runtimev1.PodSandboxStatusRequest{PodSandboxId: id, Verbose: true})
	if err != nil {
		return Sandbox{}, &RuntimeError{Reason: SandboxStatusUnavailable, Err: err}
	}
	status := response.GetStatus()
	if status.GetId() != id || status.GetState() != runtimev1.PodSandboxState_SANDBOX_READY || !matchesPod(status.GetMetadata(), pod) {
		return Sandbox{}, &RuntimeError{Reason: SandboxIdentityChanged}
	}
	// containerd marshals PodSandboxConfig with encoding/json, whose CRI proto
	// struct tags are snake_case (dns_config), not protobuf JSON's dnsConfig.
	var info struct {
		NetNamespaceClosed bool `json:"netNamespaceClosed"`
		Config             struct {
			DNS *runtimev1.DNSConfig `json:"dns_config"`
		} `json:"config"`
		RuntimeSpec struct {
			Mounts []runtimeMount `json:"mounts"`
			Linux  struct {
				Namespaces []struct{ Type, Path string } `json:"namespaces"`
			} `json:"linux"`
		} `json:"runtimeSpec"`
	}
	if err := json.Unmarshal([]byte(response.GetInfo()["info"]), &info); err != nil {
		return Sandbox{}, &RuntimeError{Reason: SandboxInfoInvalid, Err: err}
	}
	if info.NetNamespaceClosed {
		return Sandbox{}, &RuntimeError{Reason: SandboxNamespaceClosed}
	}
	var path string
	for _, ns := range info.RuntimeSpec.Linux.Namespaces {
		if ns.Type != "network" {
			continue
		}
		if path != "" {
			return Sandbox{}, &RuntimeError{Reason: SandboxNamespaceAmbiguous}
		}
		path = ns.Path
	}
	path, err = namespacePath(r.NetNSDir, path)
	if err != nil {
		return Sandbox{}, err
	}
	stat, err := os.Lstat(path)
	if err != nil || stat.Mode()&os.ModeSymlink != 0 {
		return Sandbox{}, &RuntimeError{Reason: SandboxNamespaceUnavailable, Err: err}
	}
	identity, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return Sandbox{}, &RuntimeError{Reason: SandboxNamespaceIdentityUnavailable}
	}
	if err := r.verifyRouterResolver(ctx, pod, id, resolverSource(info.RuntimeSpec.Mounts)); err != nil {
		return Sandbox{}, err
	}
	if len(info.Config.DNS.GetServers()) == 0 {
		return Sandbox{}, &RuntimeError{Reason: SandboxDNSServersUnavailable}
	}
	sandbox := Sandbox{ID: id, Path: path, Device: uint64(identity.Dev), Inode: identity.Ino,
		DNS: probe.DNSConfig{Servers: info.Config.DNS.GetServers(), Searches: info.Config.DNS.GetSearches(), Options: info.Config.DNS.GetOptions(), Source: "cri_sandbox_config_router_mount_verified"}}
	return sandbox, nil
}

func (r *CRIRuntime) verifyRouterResolver(ctx context.Context, pod *corev1.Pod, sandboxID, source string) error {
	if source == "" {
		return &RuntimeError{Reason: SandboxResolverMountUnavailable}
	}
	var id string
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != "router" {
			continue
		}
		var ok bool
		id, ok = strings.CutPrefix(status.ContainerID, "containerd://")
		if !ok {
			return &RuntimeError{Reason: RouterContainerIdentityUnavailable}
		}
	}
	if id == "" {
		return &RuntimeError{Reason: RouterContainerIdentityUnavailable}
	}
	response, err := r.Client.ContainerStatus(ctx, &runtimev1.ContainerStatusRequest{ContainerId: id, Verbose: true})
	if err != nil {
		return &RuntimeError{Reason: RouterContainerStatusUnavailable, Err: err}
	}
	status := response.GetStatus()
	if status.GetId() != id || status.GetMetadata().GetName() != "router" || status.GetState() != runtimev1.ContainerState_CONTAINER_RUNNING {
		return &RuntimeError{Reason: RouterContainerIdentityMismatch}
	}
	// containerd ContainerInfo encodes the sandbox identity as sandboxID.
	var info struct {
		SandboxID   string `json:"sandboxID"`
		Removing    bool   `json:"removing"`
		RuntimeSpec struct {
			Mounts []runtimeMount `json:"mounts"`
		} `json:"runtimeSpec"`
	}
	if err := json.Unmarshal([]byte(response.GetInfo()["info"]), &info); err != nil {
		return &RuntimeError{Reason: RouterContainerInfoUnavailable, Err: err}
	}
	if info.SandboxID != sandboxID || info.Removing {
		return &RuntimeError{Reason: RouterContainerSandboxMismatch}
	}
	actual := resolverSource(info.RuntimeSpec.Mounts)
	if actual == "" {
		return &RuntimeError{Reason: RouterResolverMountUnavailable}
	}
	if actual != source {
		return &RuntimeError{Reason: RouterResolverOverride}
	}
	return nil
}

func matchesPod(metadata *runtimev1.PodSandboxMetadata, pod *corev1.Pod) bool {
	return pod.UID != "" && metadata.GetUid() == string(pod.UID) && metadata.GetName() == pod.Name && metadata.GetNamespace() == pod.Namespace
}

func namespacePath(dir, path string) (string, error) {
	base, parent := filepath.Base(path), filepath.Dir(path)
	if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(base, "cni-") || len(base) <= 4 {
		return "", &RuntimeError{Reason: SandboxNamespaceInvalid}
	}
	if parent != dir && (dir != "/run/netns" || parent != "/var/run/netns") {
		return "", &RuntimeError{Reason: SandboxNamespaceOutsideDirectory}
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != dir {
		return "", &RuntimeError{Reason: SandboxNamespaceParentInvalid, Err: err}
	}
	return filepath.Join(dir, base), nil
}
