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

package fleet

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &Stamp{}
	_ metav1.ObjectMetaAccessor = &Stamp{}
	_ runtime.Object            = &ManagementCluster{}
	_ metav1.ObjectMetaAccessor = &ManagementCluster{}
)

func (o *Stamp) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *Stamp) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// StampList is a list of Stamp resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type StampList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Stamp `json:"items"`
}

var _ runtime.Object = &StampList{}

func (l *StampList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

func (o *ManagementCluster) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *ManagementCluster) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// ManagementClusterList is a list of ManagementCluster resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ManagementClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagementCluster `json:"items"`
}

var _ runtime.Object = &ManagementClusterList{}

func (l *ManagementClusterList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}
