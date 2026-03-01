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

package api

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &HCPOpenShiftCluster{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftCluster{}
)

func (o *HCPOpenShiftCluster) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftCluster) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

// HCPOpenShiftClusterList is a list of Clusters compatible with
// runtime.Object for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPOpenShiftCluster `json:"items"`
}

var _ runtime.Object = &HCPOpenShiftClusterList{}

func (l *HCPOpenShiftClusterList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &HCPOpenShiftClusterNodePool{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftClusterNodePool{}
)

func (o *HCPOpenShiftClusterNodePool) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftClusterNodePool) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

// HCPOpenShiftClusterNodePoolList is a list of NodePools
// compatible with runtime.Object for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftClusterNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPOpenShiftClusterNodePool `json:"items"`
}

var _ runtime.Object = &HCPOpenShiftClusterNodePoolList{}

func (l *HCPOpenShiftClusterNodePoolList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &HCPOpenShiftClusterExternalAuth{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftClusterExternalAuth{}
)

func (o *HCPOpenShiftClusterExternalAuth) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftClusterExternalAuth) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

// HCPOpenShiftClusterExternalAuthList is a list of ExternalAuths
// compatible with runtime.Object for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftClusterExternalAuthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPOpenShiftClusterExternalAuth `json:"items"`
}

var _ runtime.Object = &HCPOpenShiftClusterExternalAuthList{}

func (l *HCPOpenShiftClusterExternalAuthList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &ServiceProviderCluster{}
	_ metav1.ObjectMetaAccessor = &ServiceProviderCluster{}
)

func (o *ServiceProviderCluster) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *ServiceProviderCluster) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// ServiceProviderClusterList is a list of ServiceProviderClusters
// compatible with runtime.Object for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceProviderClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceProviderCluster `json:"items"`
}

var _ runtime.Object = &ServiceProviderClusterList{}

func (l *ServiceProviderClusterList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &Operation{}
	_ metav1.ObjectMetaAccessor = &Operation{}
)

func (o *Operation) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *Operation) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ResourceID != nil {
		om.Name = strings.ToLower(o.ResourceID.String())
	}
	return om
}

// OperationList is a list of Operations compatible with runtime.Object for use
// with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Operation `json:"items"`
}

var _ runtime.Object = &OperationList{}

func (l *OperationList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &Controller{}
	_ metav1.ObjectMetaAccessor = &Controller{}
)

func (o *Controller) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *Controller) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ResourceID != nil {
		om.Name = strings.ToLower(o.ResourceID.String())
	}
	return om
}

// ControllerList is a list of Controller resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ControllerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Controller `json:"items"`
}

var _ runtime.Object = &ControllerList{}

func (l *ControllerList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}
