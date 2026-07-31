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

package pinning

import (
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCertificates struct {
	values map[string][]byte
	errs   map[string]error
}

func (f *fakeCertificates) GetCertificate(_ context.Context, name string) ([]byte, error) {
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	return f.values[name], nil
}

type fakeApplications struct {
	applications map[string]*Application
	replaceCalls []string
	getResults   map[string][]*Application
}

func (f *fakeApplications) FindApplication(_ context.Context, displayName string) (*Application, error) {
	application := f.applications[displayName]
	if application == nil {
		return nil, errors.New("not found")
	}
	return application, nil
}

func (f *fakeApplications) ReplaceKeyCredentials(_ context.Context, applicationID, certificateName string, certificate []byte) error {
	f.replaceCalls = append(f.replaceCalls, applicationID+"="+certificateName)
	thumbprint := sha1.Sum(certificate) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	f.getResults[applicationID] = append(f.getResults[applicationID], &Application{
		ID: applicationID,
		KeyCredentials: []KeyCredential{{
			CustomKeyIdentifier: thumbprint[:],
			Type:                keyCredentialType,
			Usage:               keyCredentialUsage,
		}},
	})
	return nil
}

func (f *fakeApplications) GetApplication(_ context.Context, applicationID string) (*Application, error) {
	results := f.getResults[applicationID]
	if len(results) == 0 {
		return f.applications[applicationID], nil
	}
	result := results[0]
	f.getResults[applicationID] = results[1:]
	return result, nil
}

func TestPinSkipsMatchingCredential(t *testing.T) {
	certificate := []byte("certificate")
	thumbprint := sha1.Sum(certificate) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	apps := &fakeApplications{
		applications: map[string]*Application{
			"app": {
				ID:    "object-id",
				AppID: "client-id",
				KeyCredentials: []KeyCredential{{
					CustomKeyIdentifier: thumbprint[:],
					Type:                keyCredentialType,
					Usage:               keyCredentialUsage,
				}},
			},
		},
		getResults: map[string][]*Application{},
	}
	pinner := NewPinner(&fakeCertificates{values: map[string][]byte{"cert": certificate}}, apps)

	err := pinner.Pin(context.Background(), []Binding{{ApplicationDisplayName: "app", CertificateName: "cert"}}, Options{ReplaceAll: true, Timeout: time.Second})
	require.NoError(t, err)
	assert.Empty(t, apps.replaceCalls)
}

func TestPinReplacesAndVerifiesCredential(t *testing.T) {
	oldInterval := verificationInterval
	verificationInterval = time.Millisecond
	t.Cleanup(func() { verificationInterval = oldInterval })

	apps := &fakeApplications{
		applications: map[string]*Application{
			"app": {ID: "object-id", AppID: "client-id"},
		},
		getResults: map[string][]*Application{},
	}
	pinner := NewPinner(&fakeCertificates{values: map[string][]byte{"cert": []byte("certificate")}}, apps)

	err := pinner.Pin(context.Background(), []Binding{{ApplicationDisplayName: "app", CertificateName: "cert"}}, Options{ReplaceAll: true, Timeout: time.Second})
	require.NoError(t, err)
	assert.Equal(t, []string{"object-id=cert"}, apps.replaceCalls)
}

func TestPinDryRunDoesNotReplace(t *testing.T) {
	apps := &fakeApplications{
		applications: map[string]*Application{"app": {ID: "object-id", AppID: "client-id"}},
		getResults:   map[string][]*Application{},
	}
	pinner := NewPinner(&fakeCertificates{values: map[string][]byte{"cert": []byte("certificate")}}, apps)

	err := pinner.Pin(context.Background(), []Binding{{ApplicationDisplayName: "app", CertificateName: "cert"}}, Options{ReplaceAll: true, DryRun: true, Timeout: time.Second})
	require.NoError(t, err)
	assert.Empty(t, apps.replaceCalls)
}

func TestPinAggregatesFailures(t *testing.T) {
	pinner := NewPinner(
		&fakeCertificates{
			values: map[string][]byte{"good-cert": []byte("certificate")},
			errs:   map[string]error{"bad-cert": errors.New("denied")},
		},
		&fakeApplications{
			applications: map[string]*Application{"good-app": {ID: "object-id"}},
			getResults:   map[string][]*Application{},
		},
	)

	err := pinner.Pin(context.Background(), []Binding{
		{ApplicationDisplayName: "bad-app", CertificateName: "bad-cert"},
		{ApplicationDisplayName: "good-app", CertificateName: "good-cert"},
	}, Options{ReplaceAll: true, DryRun: true, Timeout: time.Second})
	require.ErrorContains(t, err, "bad-app")
}
