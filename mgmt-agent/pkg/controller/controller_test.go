// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func boolPtr(b bool) *bool { return &b }

func TestProcess(t *testing.T) {
	validProviderID := "azure:///subscriptions/sub-1/resourceGroups/mc-rg/providers/Microsoft.Compute/virtualMachineScaleSets/my-vmss/virtualMachines/3"

	tests := []struct {
		name         string
		node         *corev1.Node
		getVM        GetVMFunc
		wantApply    bool
		wantNICCount int64
		wantErr      bool
	}{
		{
			name: "already has capacity set, skip",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						SwiftNICResourceName: *resource.NewQuantity(7, resource.DecimalSI),
					},
				},
			},
			wantApply: false,
		},
		{
			name: "no providerID, skip",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: ""},
			},
			wantApply: false,
		},
		{
			name: "malformed providerID, skip without error",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: "not-azure:///something"},
			},
			wantApply: false,
		},
		{
			name: "getVM returns error, propagate",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("azure is down")
			},
			wantErr: true,
		},
		{
			name: "VM with 7 SWIFT NICs",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return vmResponse(
					nic(true, true),  // primary accelerated — excluded
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
					nic(false, true), // non-primary accelerated — counted
				), nil
			},
			wantApply:    true,
			wantNICCount: 7,
		},
		{
			name: "VM with no accelerated NICs",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return vmResponse(
					nic(true, false),  // primary, not accelerated
					nic(false, false), // non-primary, not accelerated
				), nil
			},
			wantApply:    true,
			wantNICCount: 0,
		},
		{
			name: "VM with nil properties",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, nil
			},
			wantApply:    true,
			wantNICCount: 0,
		},
		{
			name: "VM with nil NIC properties in list",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return armcompute.VirtualMachineScaleSetVMsClientGetResponse{
					VirtualMachineScaleSetVM: armcompute.VirtualMachineScaleSetVM{
						Properties: &armcompute.VirtualMachineScaleSetVMProperties{
							NetworkProfileConfiguration: &armcompute.VirtualMachineScaleSetVMNetworkProfileConfiguration{
								NetworkInterfaceConfigurations: []*armcompute.VirtualMachineScaleSetNetworkConfiguration{
									{Properties: nil},
									nil,
								},
							},
						},
					},
				}, nil
			},
			wantApply:    true,
			wantNICCount: 0,
		},
		{
			name: "VM with only primary accelerated NIC",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				return vmResponse(nic(true, true)), nil
			},
			wantApply:    true,
			wantNICCount: 0,
		},
		{
			name: "getVM receives correct parsed providerID coordinates",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec:       corev1.NodeSpec{ProviderID: validProviderID},
			},
			getVM: func(ctx context.Context, sub, rg, vmss, id string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
				if sub != "sub-1" {
					return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("unexpected subscription: %s", sub)
				}
				if rg != "mc-rg" {
					return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("unexpected resource group: %s", rg)
				}
				if vmss != "my-vmss" {
					return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("unexpected vmss: %s", vmss)
				}
				if id != "3" {
					return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("unexpected instance ID: %s", id)
				}
				return vmResponse(nic(false, true)), nil
			},
			wantApply:    true,
			wantNICCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &SwiftNICController{
				getVM: tt.getVM,
			}

			result, err := c.process(context.Background(), tt.node)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !tt.wantApply {
				if result != nil {
					t.Fatal("expected nil apply config, got non-nil")
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil apply config, got nil")
			}

			if result.Status == nil {
				t.Fatal("expected non-nil status in apply config")
			}

			capacity := result.Status.Capacity
			if capacity == nil {
				t.Fatal("expected non-nil capacity in apply config")
			}

			qty, exists := (*capacity)[SwiftNICResourceName]
			if !exists {
				t.Fatalf("expected %s in capacity", SwiftNICResourceName)
			}
			if got := qty.Value(); got != tt.wantNICCount {
				t.Errorf("expected NIC count %d, got %d", tt.wantNICCount, got)
			}
		})
	}
}

// nic creates a VirtualMachineScaleSetNetworkConfiguration with the given primary and accelerated flags.
func nic(primary, accelerated bool) *armcompute.VirtualMachineScaleSetNetworkConfiguration {
	return &armcompute.VirtualMachineScaleSetNetworkConfiguration{
		Properties: &armcompute.VirtualMachineScaleSetNetworkConfigurationProperties{
			Primary:                     boolPtr(primary),
			EnableAcceleratedNetworking: boolPtr(accelerated),
		},
	}
}

// vmResponse creates a VirtualMachineScaleSetVMsClientGetResponse with the given NICs.
func vmResponse(nics ...*armcompute.VirtualMachineScaleSetNetworkConfiguration) armcompute.VirtualMachineScaleSetVMsClientGetResponse {
	return armcompute.VirtualMachineScaleSetVMsClientGetResponse{
		VirtualMachineScaleSetVM: armcompute.VirtualMachineScaleSetVM{
			Properties: &armcompute.VirtualMachineScaleSetVMProperties{
				NetworkProfileConfiguration: &armcompute.VirtualMachineScaleSetVMNetworkProfileConfiguration{
					NetworkInterfaceConfigurations: nics,
				},
			},
		},
	}
}
