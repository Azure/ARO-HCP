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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	corev1 "k8s.io/api/core/v1"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/testutil"
)

type fakeCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	list      *runtimev1.ListPodSandboxResponse
	status    *runtimev1.PodSandboxStatusResponse
	container *runtimev1.ContainerStatusResponse
	listErr   error
	statusErr error
	t         *testing.T
}

func (f *fakeCRI) ContainerStatus(ctx context.Context, req *runtimev1.ContainerStatusRequest) (*runtimev1.ContainerStatusResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		f.t.Error("container status missing deadline")
	}
	if !req.Verbose || req.ContainerId != "router-id" {
		f.t.Errorf("unexpected container request: %v", req)
	}
	if f.container == nil {
		return nil, fmt.Errorf("unavailable")
	}
	return f.container, nil
}

func (f *fakeCRI) ListPodSandbox(ctx context.Context, req *runtimev1.ListPodSandboxRequest) (*runtimev1.ListPodSandboxResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		f.t.Error("CRI list missing deadline")
	}
	if req.GetFilter().GetLabelSelector()["io.kubernetes.pod.uid"] != "uid" || req.GetFilter().GetState().GetState() != runtimev1.PodSandboxState_SANDBOX_READY {
		f.t.Errorf("unexpected sandbox filter: %v", req)
	}
	return f.list, f.listErr
}

func (f *fakeCRI) PodSandboxStatus(_ context.Context, req *runtimev1.PodSandboxStatusRequest) (*runtimev1.PodSandboxStatusResponse, error) {
	if !req.Verbose || req.PodSandboxId != "sandbox" {
		f.t.Errorf("unexpected status request: %v", req)
	}
	return f.status, f.statusErr
}

func TestCRIRuntime(t *testing.T) {
	for _, scenario := range []string{"valid", "list error", "status error", "stale uid", "wrong name", "wrong namespace", "ambiguous", "not ready", "status changed", "closed", "outside", "unclean", "symlink", "no info", "no dns", "dns override", "dns missing sandbox mount", "dns missing container mount", "dns duplicate mount", "dns missing container", "dns unavailable status", "dns wrong sandbox", "dns wrong container", "dns wrong name", "dns exited", "dns removing", "dns malformed info"} {
		t.Run(scenario, func(t *testing.T) {
			dir := testutil.ResolvedTempDir(t)
			path := filepath.Join(dir, "cni-sandbox")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "router", ContainerID: "containerd://router-id"}}
			metadata := &runtimev1.PodSandboxMetadata{Uid: string(pod.UID), Name: pod.Name, Namespace: pod.Namespace}
			item := &runtimev1.PodSandbox{Id: "sandbox", Metadata: metadata, State: runtimev1.PodSandboxState_SANDBOX_READY}
			f := &fakeCRI{t: t, list: &runtimev1.ListPodSandboxResponse{Items: []*runtimev1.PodSandbox{item}}, status: &runtimev1.PodSandboxStatusResponse{Status: &runtimev1.PodSandboxStatus{Id: "sandbox", Metadata: proto.Clone(metadata).(*runtimev1.PodSandboxMetadata), State: runtimev1.PodSandboxState_SANDBOX_READY}}}
			config := &runtimev1.PodSandboxConfig{DnsConfig: &runtimev1.DNSConfig{Servers: []string{"10.0.0.10"}, Searches: []string{"ns.svc.example.test"}, Options: []string{"ndots:5"}}}
			mounts := []runtimeMount{{Destination: "/etc/resolv.conf", Source: "/var/lib/containerd/sandboxes/sandbox/resolv.conf"}}
			containerInfo := map[string]any{"sandboxID": "sandbox", "runtimeSpec": map[string]any{"mounts": mounts}, "config": map[string]any{"envs": []string{"SECRET=never-log"}}}
			f.container = &runtimev1.ContainerStatusResponse{Status: &runtimev1.ContainerStatus{Id: "router-id", Metadata: &runtimev1.ContainerMetadata{Name: "router"}, State: runtimev1.ContainerState_CONTAINER_RUNNING}}
			closed := false
			switch scenario {
			case "list error":
				f.listErr = errors.New("SECRET CRI list response")
			case "status error":
				f.statusErr = errors.New("SECRET CRI status response")
			case "stale uid":
				item.Metadata.Uid = "stale"
			case "wrong name":
				item.Metadata.Name = "other"
			case "wrong namespace":
				item.Metadata.Namespace = "other"
			case "ambiguous":
				duplicate := proto.Clone(item).(*runtimev1.PodSandbox)
				duplicate.Id = "other"
				f.list.Items = append(f.list.Items, duplicate)
			case "not ready":
				item.State = runtimev1.PodSandboxState_SANDBOX_NOTREADY
			case "status changed":
				f.status.Status.Metadata.Uid = "stale"
			case "closed":
				closed = true
			case "outside":
				path = "/tmp/cni-not-allowed"
			case "unclean":
				path = dir + "/../" + filepath.Base(dir) + "/cni-sandbox"
			case "symlink":
				link := filepath.Join(dir, "cni-link")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "no dns":
				config.DnsConfig = nil
			case "dns override":
				containerInfo["runtimeSpec"] = map[string]any{"mounts": []runtimeMount{{Destination: "/etc/resolv.conf", Source: "/custom/resolv.conf"}}}
			case "dns missing sandbox mount":
				mounts = nil
			case "dns missing container mount":
				containerInfo["runtimeSpec"] = map[string]any{}
			case "dns duplicate mount":
				containerInfo["runtimeSpec"] = map[string]any{"mounts": append(mounts, mounts...)}
			case "dns missing container":
				pod.Status.ContainerStatuses = nil
			case "dns wrong sandbox":
				containerInfo["sandboxID"] = "old-sandbox"
			case "dns wrong container":
				f.container.Status.Id = "old-container"
			case "dns wrong name":
				f.container.Status.Metadata.Name = "other"
			case "dns exited":
				f.container.Status.State = runtimev1.ContainerState_CONTAINER_EXITED
			case "dns removing":
				containerInfo["removing"] = true
			}
			containerJSON, err := json.Marshal(containerInfo)
			if err != nil {
				t.Fatal(err)
			}
			f.container.Info = map[string]string{"info": string(containerJSON)}
			if scenario == "dns malformed info" {
				f.container.Info["info"] = "{"
			}
			if scenario == "dns unavailable status" {
				f.container = nil
			}
			info, err := json.Marshal(map[string]any{"config": config, "netNamespaceClosed": closed, "runtimeSpec": map[string]any{"mounts": mounts, "linux": map[string]any{"namespaces": []map[string]string{{"type": "network", "path": path}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "valid" && !strings.Contains(string(info), `"dns_config"`) {
				t.Fatal("fixture does not use real CRI JSON encoding")
			}
			f.status.Info = map[string]string{"info": string(info)}
			if scenario == "no info" {
				f.status.Info = nil
			}
			listener, err := net.Listen("unix", filepath.Join(dir, "cri.sock"))
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			runtimev1.RegisterRuntimeServiceServer(server, f)
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			runtime, conn, err := NewRuntime("unix://"+listener.Addr().String(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			sandbox, err := runtime.Sandbox(t.Context(), pod)
			valid := scenario == "valid"
			if (err == nil) != valid {
				t.Fatalf("Sandbox() = %+v, %v; valid=%v", sandbox, err, valid)
			}
			if !valid {
				var runtimeErr *RuntimeError
				if !errors.As(err, &runtimeErr) || runtimeErr.Reason == "" {
					t.Fatalf("missing typed runtime failure: %v", err)
				}
				if (scenario == "list error" || scenario == "status error" || scenario == "dns unavailable status" || scenario == "no info" || scenario == "dns malformed info") && runtimeErr.Err == nil {
					t.Fatalf("runtime cause lost: %v", err)
				}
				if strings.Contains(err.Error(), "SECRET") {
					t.Fatalf("runtime error leaked CRI contents: %v", err)
				}
				return
			}
			if sandbox.ID != "sandbox" || sandbox.Path != path || sandbox.Inode == 0 {
				t.Fatalf("sandbox identity/config lost: %+v", sandbox)
			}
			if sandbox.DNS.Source != "cri_sandbox_config_router_mount_verified" {
				t.Fatalf("DNS not verified: %+v", sandbox)
			}
			if scenario == "valid" && (len(sandbox.DNS.Servers) != 1 || sandbox.DNS.Servers[0] != "10.0.0.10" || len(sandbox.DNS.Searches) != 1 || len(sandbox.DNS.Options) != 1) {
				t.Fatalf("DNS config lost: %+v", sandbox.DNS)
			}
		})
	}
}

func TestRuntimeErrorPreservesCause(t *testing.T) {
	cause := errors.New("private CRI response")
	err := fmt.Errorf("discover runtime: %w", &RuntimeError{Reason: SandboxListUnavailable, Err: cause})
	var runtimeErr *RuntimeError
	if !errors.Is(err, cause) || !errors.As(err, &runtimeErr) || runtimeErr.Reason != SandboxListUnavailable {
		t.Fatalf("runtime classification or cause lost: %v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("CRI response leaked: %v", err)
	}
}

func TestValidateEndpoint(t *testing.T) {
	for _, endpoint := range []string{"tcp://localhost:1234", "unix://relative", "unix:///run/../tmp/socket", "unix:///run/socket?foo", ""} {
		if ValidateEndpoint(endpoint) == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	if err := ValidateEndpoint("unix:///run/containerd/containerd.sock"); err != nil {
		t.Fatal(err)
	}
}
