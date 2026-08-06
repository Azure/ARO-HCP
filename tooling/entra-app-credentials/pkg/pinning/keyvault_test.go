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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type fakeAzureCertificateClient struct {
	getResults        [][]byte
	getErrors         []error
	operationStatuses []string
	operationError    *azcertificates.ErrorInfo
	createParameters  []azcertificates.CreateCertificateParameters
}

func (f *fakeAzureCertificateClient) GetCertificate(_ context.Context, _, _ string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	if len(f.getErrors) > 0 {
		err := f.getErrors[0]
		if len(f.getErrors) > 1 {
			f.getErrors = f.getErrors[1:]
		}
		if err != nil {
			return azcertificates.GetCertificateResponse{}, err
		}
	}
	result := f.getResults[0]
	if len(f.getResults) > 1 {
		f.getResults = f.getResults[1:]
	}
	return azcertificates.GetCertificateResponse{
		Certificate: azcertificates.Certificate{CER: result},
	}, nil
}

func (f *fakeAzureCertificateClient) CreateCertificate(_ context.Context, _ string, parameters azcertificates.CreateCertificateParameters, _ *azcertificates.CreateCertificateOptions) (azcertificates.CreateCertificateResponse, error) {
	f.createParameters = append(f.createParameters, parameters)
	return azcertificates.CreateCertificateResponse{}, nil
}

func (f *fakeAzureCertificateClient) GetCertificateOperation(_ context.Context, _ string, _ *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error) {
	status := f.operationStatuses[0]
	if len(f.operationStatuses) > 1 {
		f.operationStatuses = f.operationStatuses[1:]
	}
	return azcertificates.GetCertificateOperationResponse{
		CertificateOperation: azcertificates.CertificateOperation{
			Status: &status,
			Error:  f.operationError,
		},
	}, nil
}

func TestGetCertificateIdentifiesNotFound(t *testing.T) {
	client := &fakeAzureCertificateClient{
		getErrors: []error{&azcore.ResponseError{StatusCode: http.StatusNotFound}},
	}

	_, err := NewCertificateClient(client).GetCertificate(context.Background(), "missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCertificateNotFound))
}

func TestCreateCertificateUsesEstablishedPolicy(t *testing.T) {
	oldInterval := certificatePollInterval
	certificatePollInterval = time.Millisecond
	t.Cleanup(func() { certificatePollInterval = oldInterval })

	client := &fakeAzureCertificateClient{
		getResults:        [][]byte{[]byte("new")},
		operationStatuses: []string{"inProgress", "completed"},
	}
	certificates := NewCertificateClient(client)

	certificate, err := certificates.CreateCertificate(testContext(t), "cert", "cert.example.com", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), certificate)
	require.Len(t, client.createParameters, 1)

	policy := client.createParameters[0].CertificatePolicy
	require.NotNil(t, policy)
	assert.Equal(t, "Self", *policy.IssuerParameters.Name)
	assert.Equal(t, azcertificates.KeyTypeRSA, *policy.KeyProperties.KeyType)
	assert.Equal(t, int32(2048), *policy.KeyProperties.KeySize)
	assert.True(t, *policy.KeyProperties.Exportable)
	assert.False(t, *policy.KeyProperties.ReuseKey)
	assert.Equal(t, "application/x-pkcs12", *policy.SecretProperties.ContentType)
	assert.Equal(t, "CN=cert.example.com", *policy.X509CertificateProperties.Subject)
	assert.Equal(t, int32(120), *policy.X509CertificateProperties.ValidityInMonths)
	require.Equal(t, []*string{stringPointer("cert.example.com")}, policy.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
	require.Equal(t, []*azcertificates.KeyUsageType{
		keyUsagePointer(azcertificates.KeyUsageTypeDigitalSignature),
		keyUsagePointer(azcertificates.KeyUsageTypeKeyEncipherment),
	}, policy.X509CertificateProperties.KeyUsage)
	assert.Equal(t, azcertificates.CertificatePolicyActionAutoRenew, *policy.LifetimeActions[0].Action.ActionType)
	assert.Equal(t, int32(24), *policy.LifetimeActions[0].Trigger.LifetimePercentage)
}

func TestRotateCertificateUsesCurrentPolicy(t *testing.T) {
	oldInterval := certificatePollInterval
	certificatePollInterval = time.Millisecond
	t.Cleanup(func() { certificatePollInterval = oldInterval })

	client := &fakeAzureCertificateClient{
		getResults:        [][]byte{[]byte("old"), []byte("new")},
		operationStatuses: []string{"completed", "completed"},
	}

	certificate, err := NewCertificateClient(client).CreateCertificate(testContext(t), "cert", "new.example.com", []byte("old"))
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), certificate)
	require.Len(t, client.createParameters, 1)
	policy := client.createParameters[0].CertificatePolicy
	require.NotNil(t, policy)
	assert.Equal(t, "Self", *policy.IssuerParameters.Name)
	assert.Equal(t, "CN=new.example.com", *policy.X509CertificateProperties.Subject)
	require.Equal(t, []*string{stringPointer("new.example.com")}, policy.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
}

func TestCreateCertificateReportsOperationFailure(t *testing.T) {
	client := &fakeAzureCertificateClient{
		operationStatuses: []string{"failed"},
		operationError:    &azcertificates.ErrorInfo{Code: "IssuerFailure"},
	}

	_, err := NewCertificateClient(client).CreateCertificate(testContext(t), "cert", "cert.example.com", nil)
	require.ErrorContains(t, err, `status "failed"`)
	require.ErrorContains(t, err, "IssuerFailure")
}

func TestCreateCertificateRequiresDeadline(t *testing.T) {
	_, err := NewCertificateClient(&fakeAzureCertificateClient{}).CreateCertificate(
		context.Background(),
		"cert",
		"cert.example.com",
		nil,
	)
	require.ErrorContains(t, err, "context must have a deadline")
}

func TestCreateCertificateTimesOutWaitingForOperation(t *testing.T) {
	oldInterval := certificatePollInterval
	certificatePollInterval = time.Millisecond
	t.Cleanup(func() { certificatePollInterval = oldInterval })

	client := &fakeAzureCertificateClient{
		operationStatuses: []string{"inProgress"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	t.Cleanup(cancel)

	_, err := NewCertificateClient(client).CreateCertificate(ctx, "cert", "cert.example.com", nil)
	require.ErrorContains(t, err, "wait for certificate operation")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCreateCertificateTimesOutWaitingForNewCertificate(t *testing.T) {
	oldInterval := certificatePollInterval
	certificatePollInterval = time.Millisecond
	t.Cleanup(func() { certificatePollInterval = oldInterval })

	client := &fakeAzureCertificateClient{
		getResults:        [][]byte{[]byte("old")},
		operationStatuses: []string{"completed"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	t.Cleanup(cancel)

	_, err := NewCertificateClient(client).CreateCertificate(ctx, "cert", "cert.example.com", []byte("old"))
	require.ErrorContains(t, err, "wait for created certificate")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func stringPointer(value string) *string {
	return &value
}

func keyUsagePointer(value azcertificates.KeyUsageType) *azcertificates.KeyUsageType {
	return &value
}
