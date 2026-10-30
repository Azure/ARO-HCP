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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

const defaultCertificatePollInterval = 2 * time.Second

type azureCertificateClient interface {
	GetCertificate(ctx context.Context, name, version string, options *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	GetCertificateOperation(ctx context.Context, name string, options *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error)
	CreateCertificate(ctx context.Context, name string, parameters azcertificates.CreateCertificateParameters, options *azcertificates.CreateCertificateOptions) (azcertificates.CreateCertificateResponse, error)
}

type certificateClient struct {
	client       azureCertificateClient
	pollInterval time.Duration
}

// CertificateClientOption configures a certificate client.
type CertificateClientOption func(*certificateClient)

// WithPollInterval sets how long to wait between Key Vault reads while a
// certificate is being created.
func WithPollInterval(interval time.Duration) CertificateClientOption {
	return func(c *certificateClient) {
		c.pollInterval = interval
	}
}

// NewCertificateClient adapts the Azure Key Vault SDK client.
func NewCertificateClient(client azureCertificateClient, options ...CertificateClientOption) CertificateClient {
	certificates := &certificateClient{client: client, pollInterval: defaultCertificatePollInterval}
	for _, option := range options {
		option(certificates)
	}
	return certificates
}

// ErrCertificateNotFound identifies a missing Key Vault certificate.
var ErrCertificateNotFound = errors.New("certificate not found in Key Vault")

func (c *certificateClient) GetCertificate(ctx context.Context, name string) ([]byte, error) {
	response, err := c.client.GetCertificate(ctx, name, "", nil)
	if err == nil {
		return response.CER, nil
	}

	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrCertificateNotFound, name)
	}
	return nil, fmt.Errorf("get certificate: %w", err)
}

func (c *certificateClient) CreateCertificate(ctx context.Context, name, dnsName string, previousCertificate []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return nil, fmt.Errorf("create certificate: context must have a deadline")
	}

	parameters := azcertificates.CreateCertificateParameters{
		CertificatePolicy: certificatePolicy(dnsName),
	}
	_, err := c.client.CreateCertificate(ctx, name, parameters, nil)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	err = c.pollForCertificateOperation(ctx, name)
	if err != nil {
		return nil, err
	}

	var certificate []byte
	err = wait.PollUntilContextCancel(ctx, c.pollInterval, true, func(ctx context.Context) (bool, error) {
		current, err := c.GetCertificate(ctx, name)
		switch {
		case errors.Is(err, ErrCertificateNotFound):
			return false, nil
		case err != nil:
			return false, err
		}
		if len(current) == 0 || bytes.Equal(current, previousCertificate) {
			return false, nil
		}
		certificate = current
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("wait for created certificate: %w", err)
	}
	return certificate, nil
}

func (c *certificateClient) pollForCertificateOperation(ctx context.Context, name string) error {
	err := wait.PollUntilContextCancel(ctx, c.pollInterval, true, func(ctx context.Context) (bool, error) {
		operation, err := c.client.GetCertificateOperation(ctx, name, nil)
		if err != nil {
			return false, fmt.Errorf("get certificate operation: %w", err)
		}
		if operation.Status == nil {
			return false, fmt.Errorf("get certificate operation: Key Vault returned no status")
		}

		switch *operation.Status {
		case "completed":
			return true, nil
		case "inProgress":
			return false, nil
		case "cancelled":
			return false, fmt.Errorf("certificate operation was cancelled")
		}
		if operation.Error != nil {
			return false, fmt.Errorf("certificate operation failed with status %q and code %q", *operation.Status, operation.Error.Code)
		}
		return false, fmt.Errorf("certificate operation failed with status %q", *operation.Status)
	})
	if err != nil {
		return fmt.Errorf("wait for certificate operation: %w", err)
	}
	return nil
}

func certificatePolicy(dnsName string) *azcertificates.CertificatePolicy {
	autoRenew := azcertificates.CertificatePolicyActionAutoRenew
	keyType := azcertificates.KeyTypeRSA
	digitalSignature := azcertificates.KeyUsageTypeDigitalSignature
	keyEncipherment := azcertificates.KeyUsageTypeKeyEncipherment
	return &azcertificates.CertificatePolicy{
		IssuerParameters: &azcertificates.IssuerParameters{
			Name: to.Ptr("Self"),
		},
		KeyProperties: &azcertificates.KeyProperties{
			Exportable: to.Ptr(true),
			KeySize:    to.Ptr(int32(2048)),
			KeyType:    &keyType,
			ReuseKey:   to.Ptr(false),
		},
		LifetimeActions: []*azcertificates.LifetimeAction{{
			Action: &azcertificates.LifetimeActionType{
				ActionType: &autoRenew,
			},
			Trigger: &azcertificates.LifetimeActionTrigger{
				LifetimePercentage: to.Ptr(int32(24)),
			},
		}},
		SecretProperties: &azcertificates.SecretProperties{
			ContentType: to.Ptr("application/x-pkcs12"),
		},
		X509CertificateProperties: &azcertificates.X509CertificateProperties{
			KeyUsage: []*azcertificates.KeyUsageType{
				&digitalSignature,
				&keyEncipherment,
			},
			Subject: to.Ptr("CN=" + dnsName),
			SubjectAlternativeNames: &azcertificates.SubjectAlternativeNames{
				DNSNames: []*string{to.Ptr(dnsName)},
			},
			ValidityInMonths: to.Ptr(int32(120)),
		},
	}
}
