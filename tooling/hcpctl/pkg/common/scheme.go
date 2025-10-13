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

package common

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

var globalScheme *runtime.Scheme

func init() {
	globalScheme = runtime.NewScheme()

	// Add HyperShift APIs
	if err := hypershiftv1beta1.AddToScheme(globalScheme); err != nil {
		panic(fmt.Errorf("failed to add hypershift scheme: %w", err))
	}

	// Add HyperShift certificates APIs
	if err := certificatesv1alpha1.AddToScheme(globalScheme); err != nil {
		panic(fmt.Errorf("failed to add certificates scheme: %w", err))
	}
}

// Scheme returns a global runtime scheme with all necessary API groups registered.
// This scheme is safe for concurrent use and expensive to create, so it's created once.
func Scheme() *runtime.Scheme {
	return globalScheme
}
