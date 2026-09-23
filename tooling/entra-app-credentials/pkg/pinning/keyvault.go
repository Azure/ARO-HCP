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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type azureCertificateClient interface {
	GetCertificate(ctx context.Context, name, version string, options *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	GetCertificateOperation(ctx context.Context, name string, options *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error)
	CreateCertificate(ctx context.Context, name string, parameters azcertificates.CreateCertificateParameters, options *azcertificates.CreateCertificateOptions) (azcertificates.CreateCertificateResponse, error)
}

type certificateClient struct {
	client azureCertificateClient
}

// NewCertificateClient adapts the Azure Key Vault SDK client.
func NewCertificateClient(client azureCertificateClient) CertificateClient {
	return &certificateClient{client: client}
}

// ErrCertificateNotFound identifies a missing Key Vault certificate.
var ErrCertificateNotFound = errors.New("certificate not found in Key Vault")

var certificatePollInterval = 2 * time.Second

func (c *certificateClient) GetCertificate(ctx context.Context, name string) ([]byte, error) {
	response, err := c.client.GetCertificate(ctx, name, "", nil)
	if err != nil {
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrCertificateNotFound, name)
		}
		return nil, fmt.Errorf("get certificate: %w", err)
	}
	return response.CER, nil
}

func (c *certificateClient) CreateCertificate(ctx context.Context, name, dnsName string, previousCertificate []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return nil, fmt.Errorf("create certificate: context must have a deadline")
	}

	parameters := azcertificates.CreateCertificateParameters{
		CertificatePolicy: certificatePolicy(dnsName),
	}
	if _, err := c.client.CreateCertificate(ctx, name, parameters, nil); err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	if err := c.pollForCertificateOperation(ctx, name); err != nil {
		return nil, err
	}

	for {
		certificate, err := c.GetCertificate(ctx, name)
		if err == nil && len(certificate) > 0 && !bytes.Equal(certificate, previousCertificate) {
			return certificate, nil
		}
		if err != nil && !errors.Is(err, ErrCertificateNotFound) {
			return nil, fmt.Errorf("wait for created certificate: %w", err)
		}
		if err := waitForCertificatePoll(ctx); err != nil {
			return nil, fmt.Errorf("wait for created certificate: %w", err)
		}
	}
}

func (c *certificateClient) pollForCertificateOperation(ctx context.Context, name string) error {
	for {
		operation, err := c.client.GetCertificateOperation(ctx, name, nil)
		if err != nil {
			return fmt.Errorf("get certificate operation: %w", err)
		}
		if operation.Status == nil {
			return fmt.Errorf("get certificate operation: Key Vault returned no status")
		}
		switch *operation.Status {
		case "completed":
			return nil
		case "inProgress":
			if err := waitForCertificatePoll(ctx); err != nil {
				return fmt.Errorf("wait for certificate operation: %w", err)
			}
		case "cancelled":
			return fmt.Errorf("certificate operation was cancelled")
		default:
			if operation.Error != nil {
				return fmt.Errorf("certificate operation failed with status %q and code %q", *operation.Status, operation.Error.Code)
			}
			return fmt.Errorf("certificate operation failed with status %q", *operation.Status)
		}
	}
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

func waitForCertificatePoll(ctx context.Context) error {
	timer := time.NewTimer(certificatePollInterval)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
