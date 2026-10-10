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
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

// IngressTLSCertificateControllerName is the single source of the controller name
// used for metrics, logging, and controller docs. Not to be confused with the
// unrelated k8sresources.IngressCertificateControllerName, which delivers the
// certificate this controller observes into the HostedCluster namespace.
const IngressTLSCertificateControllerName = "IngressTLSCertificate"

// ingressTLSCertificateField configures a tlsCertificateSyncer to observe the ingress
// TLS certificate.
var ingressTLSCertificateField = tlsCertificateField{
	label: "ingress",
	get:   func(resources *coreapi.AzureResources) *coreapi.TLSCertificate { return resources.IngressCertificate },
	set: func(resources *coreapi.AzureResources, certificate *coreapi.TLSCertificate) {
		resources.IngressCertificate = certificate
	},
}

// NewIngressTLSCertificateController builds the tlsCertificateSyncer instance (see
// tls_certificate_sync.go) that observes the ingress TLS certificate.
func NewIngressTLSCertificateController(resourcesDBClient corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, managementClusterLister fleetlisters.ManagementClusterLister, clients *azureclient.BackendIdentityAzureClients) controllerutils.Controller {
	return newTLSCertificateController(IngressTLSCertificateControllerName, ingressTLSCertificateField, resourcesDBClient, informers, managementClusterLister, clients)
}
