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
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
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

// HCPOpenShiftClusterList is a list of HCPOpenShiftClusters compatible with
// runtime.Object for use with Kubernetes informer machinery.
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

// HCPOpenShiftClusterNodePoolList is a list of HCPOpenShiftClusterNodePools
// compatible with runtime.Object for use with Kubernetes informer machinery.
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
type OperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Operation `json:"items"`
}

var _ runtime.Object = &OperationList{}

func (l *OperationList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}
