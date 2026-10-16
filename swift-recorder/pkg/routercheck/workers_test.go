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
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // Deployed CAPI resources use v1beta1.
)

func workerInput(name, cluster string, addresses ...string) WorkerInput {
	machine := &clusterv1.Machine{TypeMeta: metav1.TypeMeta{APIVersion: "cluster.x-k8s.io/v1beta1", Kind: "Machine"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name + "-uid"), Labels: map[string]string{clusterNameLabel: cluster}},
		Spec:       clusterv1.MachineSpec{ClusterName: cluster, InfrastructureRef: corev1.ObjectReference{APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1", Kind: "AzureMachine", Name: name + "-azure"}}}
	azure := &capzv1.AzureMachine{TypeMeta: metav1.TypeMeta{APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1", Kind: "AzureMachine"},
		ObjectMeta: metav1.ObjectMeta{Name: name + "-azure", Namespace: "ns", UID: types.UID(name + "-azure-uid"), Labels: map[string]string{clusterNameLabel: cluster}, OwnerReferences: []metav1.OwnerReference{{APIVersion: machine.APIVersion, Kind: "Machine", Name: name, UID: machine.UID}}},
		Spec:       capzv1.AzureMachineSpec{ProviderID: ptr.To("azure:///" + name)}}
	for _, address := range addresses {
		azure.Status.Addresses = append(azure.Status.Addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: address})
	}
	return WorkerInput{Machine: machine, AzureMachine: azure}
}

func workerObjects(name, cluster string, addresses ...string) (*unstructured.Unstructured, *unstructured.Unstructured) {
	worker := workerInput(name, cluster, addresses...)
	machine, err := runtime.DefaultUnstructuredConverter.ToUnstructured(worker.Machine)
	if err != nil {
		panic(err)
	}
	azure, err := runtime.DefaultUnstructuredConverter.ToUnstructured(worker.AzureMachine)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: machine}, &unstructured.Unstructured{Object: azure}
}

func TestWorkerTargets(t *testing.T) {
	for _, scenario := range []string{"before registration", "deleting", "corroborated", "stale machine", "no address", "invalid address", "no source", "wrong family", "no workers"} {
		t.Run(scenario, func(t *testing.T) {
			worker := workerInput("worker", "infra", "10.2.0.4")
			workers := []WorkerInput{worker}
			swift := []SwiftInterface{{IP: "10.1.0.1"}}
			valid := false
			switch scenario {
			case "before registration":
				valid = true
			case "deleting":
				worker.Machine.DeletionTimestamp = ptr.To(metav1.Now())
				valid = true
			case "corroborated", "stale machine":
				address := "10.2.0.4"
				if scenario == "stale machine" {
					address = "10.2.0.99"
				}
				worker.Machine.Status.Addresses = []clusterv1.MachineAddress{{Type: clusterv1.MachineInternalIP, Address: address}}
				valid = true
			case "no address":
				worker.AzureMachine.Status.Addresses = nil
			case "invalid address":
				worker.AzureMachine.Status.Addresses[0].Address = "127.0.0.1"
			case "no source":
				swift = nil
			case "wrong family":
				swift[0].IP = "fd01::1"
			case "no workers":
				workers = nil
			}
			targets, err := workerTargets(workers, swift)
			if (err == nil) != valid {
				t.Fatalf("valid=%t targets=%+v error=%v", valid, targets, err)
			}
			if !valid {
				if !errors.Is(err, ErrDiscoveryData) || len(targets) != 0 {
					t.Fatalf("partial targets or untyped error: %v %v", targets, err)
				}
				return
			}
			if len(targets) != 1 {
				t.Fatalf("targets=%+v", targets)
			}
			info := targets[0]
			if !info.Target.TCPOnly || info.Target.Port != 22 || info.Target.SourceIP != "10.1.0.1" || info.Target.Address != "10.2.0.4" || info.Target.ServerName != "" {
				t.Fatalf("TCP binding: %+v", info)
			}
			evidence := info.Workers[0]
			if evidence.MachineUID != "worker-uid" || evidence.AzureMachineUID != "worker-azure-uid" || evidence.ClusterName != "infra" || evidence.ProviderID != "azure:///worker" {
				t.Fatalf("identity: %+v", evidence)
			}
			if scenario == "deleting" && (!info.Deleting || !evidence.MachineDeleting) {
				t.Fatalf("deletion evidence: %+v", info)
			}
			if scenario == "corroborated" && (evidence.MachineAddressMatches == nil || !*evidence.MachineAddressMatches) {
				t.Fatal("corroboration lost")
			}
			if scenario == "stale machine" && (evidence.MachineAddressMatches == nil || *evidence.MachineAddressMatches) {
				t.Fatal("stale data hidden")
			}
		})
	}
}

func TestWorkerTargetsAllAddressesAndEvidence(t *testing.T) {
	var workers []WorkerInput
	for i := range 140 {
		workers = append(workers, workerInput(fmt.Sprintf("worker-%d", i), "infra", fmt.Sprintf("10.2.0.%d", i+1), "10.2.1.1", "10.2.1.1", "fd02::1", "fd02:0::1"))
	}
	targets, err := workerTargets(workers, []SwiftInterface{{IP: "10.1.0.1"}, {IP: "10.1.0.2"}, {IP: "10.1.0.1"}, {IP: "fd01::1"}})
	if err != nil || len(targets) != 283 {
		t.Fatalf("workers capped or not deduplicated: %d %v", len(targets), err)
	}
	for _, info := range targets {
		if !sameFamily(info.Target.Address, info.Target.SourceIP) {
			t.Fatalf("cross-family binding: %+v", info)
		}
		if (info.Target.Address == "10.2.1.1" || info.Target.Address == "fd02::1") && len(info.Workers) != 140 {
			t.Fatalf("shared identity lost: %+v", info)
		}
	}
}

func TestGatherWorkerReferences(t *testing.T) {
	for _, scenario := range []string{"explicit namespace", "wrong namespace", "wrong owner UID", "wrong ref UID", "wrong cluster", "mixed clusters", "unrelated azure"} {
		t.Run(scenario, func(t *testing.T) {
			input := discoveryInput(t)
			worker := input.Workers[0]
			valid := false
			switch scenario {
			case "explicit namespace":
				worker.Machine.Spec.InfrastructureRef.Namespace = "ns"
				valid = true
			case "wrong namespace":
				worker.Machine.Spec.InfrastructureRef.Namespace = "other"
			case "wrong owner UID":
				worker.AzureMachine.OwnerReferences[0].UID = "old"
			case "wrong ref UID":
				worker.Machine.Spec.InfrastructureRef.UID = "old"
			case "wrong cluster":
				worker.AzureMachine.Labels[clusterNameLabel] = "other"
			case "mixed clusters":
				input.Workers = append(input.Workers, workerInput("other", "other-infra", "10.3.0.1"))
			case "unrelated azure":
				valid = true
			}
			s := cachedDiscoveryInput(t, input)
			if scenario == "unrelated azure" {
				_, azure := workerObjects("orphan", "other-infra", "10.3.0.1")
				if err := s.objects[azureMachineGVR].Informer().GetIndexer().Add(azure); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Gather(t.Context(), s, input.Pod, input.DNS)
			if (err == nil) != valid {
				t.Fatalf("valid=%t error=%v", valid, err)
			}
		})
	}
}
