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

package arm

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &Subscription{}
	_ metav1.ObjectMetaAccessor = &Subscription{}
)

func (s *Subscription) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

// GetObjectMeta returns metadata that allows Kubernetes informer machinery
// to key subscriptions by their subscription ID.
func (s *Subscription) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if s.ResourceID != nil {
		om.Name = strings.ToLower(s.ResourceID.String())
	}
	return om
}
