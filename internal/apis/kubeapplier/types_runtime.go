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

package kubeapplier

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &ApplyDesire{}
	_ metav1.ObjectMetaAccessor = &ApplyDesire{}
)

func (o *ApplyDesire) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *ApplyDesire) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// ApplyDesireList is a list of ApplyDesire resources compatible with runtime.Object
// for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ApplyDesireList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplyDesire `json:"items"`
}

var _ runtime.Object = &ApplyDesireList{}

func (l *ApplyDesireList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &DeleteDesire{}
	_ metav1.ObjectMetaAccessor = &DeleteDesire{}
)

func (o *DeleteDesire) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *DeleteDesire) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// DeleteDesireList is a list of DeleteDesire resources compatible with runtime.Object
// for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DeleteDesireList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeleteDesire `json:"items"`
}

var _ runtime.Object = &DeleteDesireList{}

func (l *DeleteDesireList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

var (
	_ runtime.Object            = &ReadDesire{}
	_ metav1.ObjectMetaAccessor = &ReadDesire{}
)

func (o *ReadDesire) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *ReadDesire) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.GetResourceID() != nil {
		om.Name = strings.ToLower(o.GetResourceID().String())
	}
	return om
}

// ReadDesireList is a list of ReadDesire resources compatible with runtime.Object
// for use with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ReadDesireList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReadDesire `json:"items"`
}

var _ runtime.Object = &ReadDesireList{}

func (l *ReadDesireList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}
