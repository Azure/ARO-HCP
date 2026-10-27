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

// Package certificates implements Key Vault certificate cleanup steps: soft
// deleting policy-selected active certificates and purging deleted ones.
package certificates

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

const certificateType = "Microsoft.KeyVault/vaults/certificates"

// Client is the subset of the Key Vault certificates data-plane client used by the steps.
type Client interface {
	NewListCertificatePropertiesPager(*azcertificates.ListCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListCertificatePropertiesResponse]
	NewListDeletedCertificatePropertiesPager(*azcertificates.ListDeletedCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListDeletedCertificatePropertiesResponse]
	GetCertificate(context.Context, string, string, *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	GetDeletedCertificate(context.Context, string, *azcertificates.GetDeletedCertificateOptions) (azcertificates.GetDeletedCertificateResponse, error)
	DeleteCertificate(context.Context, string, *azcertificates.DeleteCertificateOptions) (azcertificates.DeleteCertificateResponse, error)
	PurgeDeletedCertificate(context.Context, string, *azcertificates.PurgeDeletedCertificateOptions) (azcertificates.PurgeDeletedCertificateResponse, error)
}

// ResourceGroupLister lists the resource groups of one subscription.
type ResourceGroupLister interface {
	NewListPager(*armresources.ResourceGroupsClientListOptions) *runtime.Pager[armresources.ResourceGroupsClientListResponse]
}

// certificateID extracts the certificate name and optional version from a
// certificate ID, requiring it to belong to the configured vault.
func certificateID(vaultHost string, id *azcertificates.ID) (name, version string, valid bool) {
	if id == nil {
		return "", "", false
	}
	u, err := url.Parse(string(*id))
	if err != nil || !ownVaultURL(u, vaultHost) {
		return "", "", false
	}
	parts := strings.Split(u.Path, "/")
	if (len(parts) != 3 && len(parts) != 4) || parts[0] != "" || parts[1] != "certificates" || parts[2] == "" {
		return "", "", false
	}
	if len(parts) == 4 {
		version = parts[3]
		if version == "" {
			return "", "", false
		}
	}
	return parts[2], version, true
}

func deletedCertificateID(vaultHost string, id *string) (name string, valid bool) {
	if id == nil {
		return "", false
	}
	u, err := url.Parse(*id)
	if err != nil || !ownVaultURL(u, vaultHost) {
		return "", false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 3 || parts[0] != "" || parts[1] != "deletedcertificates" || parts[2] == "" {
		return "", false
	}
	return parts[2], true
}

func ownVaultURL(u *url.URL, vaultHost string) bool {
	return u.Scheme == "https" && u.Host == vaultHost && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == ""
}

func sameString(a, b *string) bool {
	return a != nil && b != nil && *a == *b
}

func sameTime(a, b *time.Time) bool {
	return a != nil && b != nil && a.Equal(*b)
}

func isNotFound(err error) bool {
	var response *azcore.ResponseError
	return errors.As(err, &response) && response.StatusCode == http.StatusNotFound
}

func deletedProperties(latest azcertificates.DeletedCertificate) *azcertificates.DeletedCertificateProperties {
	return &azcertificates.DeletedCertificateProperties{
		Attributes:         latest.Attributes,
		ID:                 latest.ID,
		RecoveryID:         latest.RecoveryID,
		Tags:               latest.Tags,
		X509Thumbprint:     latest.X509Thumbprint,
		DeletedDate:        latest.DeletedDate,
		ScheduledPurgeDate: latest.ScheduledPurgeDate,
	}
}
