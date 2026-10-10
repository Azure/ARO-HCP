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

package azureresources

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

// tlsCertificatesClient is the subset of the Key Vault certificates client needed to
// observe TLS certificates created by Cluster Service, shared by the
// KubeAPIServerTLSCertificate and IngressTLSCertificate controllers.
type tlsCertificatesClient interface {
	GetCertificate(context.Context, string, string, *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	GetCertificateOperation(context.Context, string, *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error)
}

// observeTLSCertificateDeleted reports whether the named Azure Key Vault certificate has
// already been deleted.
func observeTLSCertificateDeleted(ctx context.Context, client tlsCertificatesClient, name string) (bool, error) {
	_, err := client.GetCertificate(ctx, name, "", nil)
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// observeTLSCertificate reports whether the named Azure Key Vault certificate is ready
// (i.e. a confirmed CS-created certificate), matching Cluster Service's own readiness
// check: GET the certificate to confirm existence, then GET its operation.
func observeTLSCertificate(ctx context.Context, client tlsCertificatesClient, name string) (bool, error) {
	_, err := client.GetCertificate(ctx, name, "", nil)
	var responseError *azcore.ResponseError
	certificateMissing := errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
	if err != nil && !certificateMissing {
		return false, err
	}
	operation, err := client.GetCertificateOperation(ctx, name, nil)
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		//  Azure Key Vault can return a 200 OK for GetCertificate while a concurrent or subsequent request
		//  to the pending endpoint returns 404 Not Found. This occurs because pending certificate operations
		// are transient resources managed separately from the final certificate object.
		// If this is the case, consider the certificate to be present since GetCertificate returned a 200 OK.
		if !certificateMissing {
			return true, nil
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if operation.Status == nil {
		return false, fmt.Errorf("certificate %q operation has no status", name)
	}
	switch *operation.Status {
	case "completed":
		return !certificateMissing, nil
	case "inProgress":
		return false, nil
	case "failed", "cancelled":
		return false, fmt.Errorf("certificate %q operation %s", name, *operation.Status)
	default:
		return false, fmt.Errorf("certificate %q has unknown operation status %q", name, *operation.Status)
	}
}
