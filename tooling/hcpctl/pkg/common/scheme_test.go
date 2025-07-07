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
	"testing"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHypershiftScheme(t *testing.T) {
	scheme, err := NewHypershiftScheme()
	require.NoError(t, err)
	require.NotNil(t, scheme)

	// Verify that the scheme recognizes HyperShift cluster types
	gvks, _, err := scheme.ObjectKinds(&hypershiftv1beta1.HostedCluster{})
	require.NoError(t, err)
	assert.Len(t, gvks, 1)
	assert.Equal(t, "hypershift.openshift.io", gvks[0].Group)
	assert.Equal(t, "v1beta1", gvks[0].Version)
	assert.Equal(t, "HostedCluster", gvks[0].Kind)
}

func TestNewCertificatesScheme(t *testing.T) {
	scheme, err := NewCertificatesScheme()
	require.NoError(t, err)
	require.NotNil(t, scheme)

	// Verify that the scheme recognizes HyperShift certificate types
	gvks, _, err := scheme.ObjectKinds(&certificatesv1alpha1.CertificateSigningRequestApproval{})
	require.NoError(t, err)
	assert.Len(t, gvks, 1)
	assert.Equal(t, "certificates.hypershift.openshift.io", gvks[0].Group)
	assert.Equal(t, "v1alpha1", gvks[0].Version)
	assert.Equal(t, "CertificateSigningRequestApproval", gvks[0].Kind)
}
