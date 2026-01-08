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

package mc

import (
	"context"
	"fmt"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"
	certificatesclientv1alpha1 "github.com/openshift/hypershift/client/clientset/clientset/typed/certificates/v1alpha1"
)

const (
	defaultExpirationSeconds = int32(86353) // ~24 hours

	// AnnotationCSRDigest is used to track the digest of the CSR inputs (private key + subject)
	AnnotationCSRDigest = "sessiongate.aro-hcp.azure.com/csr-digest"

	// AnnotationClusterReference is used to track the cluster cluster backref for a HostedControlPlane CR
	AnnotationClusterReference = "hypershift.openshift.io/cluster"
)

func parseClusterRef(clusterRef string) (string, string, error) {
	parts := strings.Split(clusterRef, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid cluster reference: %s", clusterRef)
	}
	return parts[0], parts[1], nil
}

// ManagementClusterProvider provides access to management cluster resources
type ManagementClusterQuerier interface {
	GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error)
	GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error)
	GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
}

type ManagementClusterProviderBuilder func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error)

func NewAKSManagermentClusterBuilder(azureCredentials azcore.TokenCredential) ManagementClusterProviderBuilder {
	// todo: informers and instance caching
	return func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error) {
		kubeConfig, err := GetAKSRESTConfig(ctx, resourceId, azureCredentials)
		if err != nil {
			return nil, fmt.Errorf("failed to get AKS REST config: %w", err)
		}
		kubeClient, err := kubernetes.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		hypershiftClientset, err := hypershiftclientset.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create hypershift clientset: %w", err)
		}
		certificatesClientset, err := certificatesclientv1alpha1.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificates clientset: %w", err)
		}
		return &ManagementClusterProvider{
			HypershiftClient:   hypershiftClientset,
			CertificatesClient: certificatesClientset,
			KubeClient:         kubeClient,
		}, nil
	}
}

// managementClusterProvider implements ManagementClusterProvider
type ManagementClusterProvider struct {
	HypershiftClient   hypershiftclientset.Interface
	CertificatesClient certificatesclientv1alpha1.CertificatesV1alpha1Interface
	KubeClient         kubernetes.Interface
}

func (d *ManagementClusterProvider) getHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	hcpList, err := d.HypershiftClient.HypershiftV1beta1().HostedControlPlanes(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	if len(hcpList.Items) == 0 {
		return nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hostedcontrolplanes"},
			namespace,
		)
	}
	if len(hcpList.Items) > 1 {
		return nil, fmt.Errorf("multiple HostedControlPlane found for namespace %s", namespace)
	}
	hcp := hcpList.Items[0]
	return &hcp, nil
}

// GetHostedCluster finds the HostedCluster CR for a given HostedControlPlane namespace.
func (d *ManagementClusterProvider) GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	hcp, err := d.getHostedControlPlane(ctx, namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}
	hcRef := hcp.Annotations[AnnotationClusterReference]
	hcNamespace, hcName, err := parseClusterRef(hcRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cluster reference: %w", err)
	}
	hc, err := d.HypershiftClient.HypershiftV1beta1().HostedClusters(hcNamespace).Get(ctx, hcName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedCluster: %w", err)
	}
	return hc, nil
}

func (d *ManagementClusterProvider) GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error) {
	return d.KubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
}

func (d *ManagementClusterProvider) GetCSRApproval(ctx context.Context, hostedControlPlaneNamespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	return d.CertificatesClient.CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Get(ctx, name, metav1.GetOptions{})
}
