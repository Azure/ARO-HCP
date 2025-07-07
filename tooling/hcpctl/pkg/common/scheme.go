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

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NewHypershiftScheme creates a new runtime scheme with HyperShift cluster APIs registered.
// This is used for working with HostedCluster resources.
func NewHypershiftScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := hypershiftv1beta1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add hypershift scheme: %w", err)
	}
	return scheme, nil
}

// NewCertificatesScheme creates a new runtime scheme with HyperShift certificates APIs registered.
// This is used for working with CertificateSigningRequestApproval resources.
func NewCertificatesScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := certificatesv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add certificates scheme: %w", err)
	}
	return scheme, nil
}

// NewEmptyScheme creates a new empty runtime scheme for testing purposes.
// This is commonly used in unit tests with fake clients.
func NewEmptyScheme() *runtime.Scheme {
	return runtime.NewScheme()
}
