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

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"
	certificatesclientv1alpha1 "github.com/openshift/hypershift/client/clientset/clientset/typed/certificates/v1alpha1"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"
)

const (
	// AnnotationCSRDigest is used to track the digest of the CSR inputs (private key + subject)
	AnnotationCSRDigest = "sessiongate.aro-hcp.azure.com/csr-digest"

	// AnnotationClusterReference is used to track the cluster cluster backref for a HostedControlPlane CR
	AnnotationClusterReference = "hypershift.openshift.io/cluster"
)

// ManagementClusterProvider provides access to management cluster resources
type ManagementClusterQuerier interface {
	GetHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error)
	GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error)
	GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
}

type ManagementClusterProviderBuilder func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error)

// NewAKSManagermentClusterBuilder creates a builder that constructs ManagementClusterProvider instances for AKS clusters.
func NewAKSManagermentClusterBuilder(azureCredentials azcore.TokenCredential) ManagementClusterProviderBuilder {
	return func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error) {
		kubeConfig, err := mc.GetAKSRESTConfig(ctx, resourceId, azureCredentials)
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
			HypershiftClient: hypershiftClientset,
			HypershiftInformers: hypershiftinformers.NewSharedInformerFactoryWithOptions(
				hypershiftClientset,
				time.Second*300,
			),
			CertificatesClient: certificatesClientset,
			KubeClient:         kubeClient,
			KubeInformers: kubeinformers.NewSharedInformerFactoryWithOptions(
				kubeClient,
				time.Second*300,
				kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
					opts.LabelSelector = ManagedByLabelSelector()
				}),
			),
			stopCh: make(chan struct{}),
		}, nil
	}
}

// managementClusterProvider implements ManagementClusterProvider
type ManagementClusterProvider struct {
	HypershiftClient    hypershiftclientset.Interface
	HypershiftInformers hypershiftinformers.SharedInformerFactory
	CertificatesClient  certificatesclientv1alpha1.CertificatesV1alpha1Interface
	KubeClient          kubernetes.Interface
	KubeInformers       kubeinformers.SharedInformerFactory
	stopCh              chan struct{}
}

func (d *ManagementClusterProvider) GetHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	hcpList, err := d.HypershiftInformers.Hypershift().V1beta1().HostedControlPlanes().Lister().HostedControlPlanes(namespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	if len(hcpList) == 0 {
		return nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hostedcontrolplanes"},
			namespace,
		)
	}
	if len(hcpList) > 1 {
		return nil, fmt.Errorf("multiple HostedControlPlane found for namespace %s", namespace)
	}
	return hcpList[0], nil
}

func (d *ManagementClusterProvider) GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error) {
	return d.KubeInformers.Certificates().V1().CertificateSigningRequests().Lister().Get(name)
}

func (d *ManagementClusterProvider) GetCSRApproval(ctx context.Context, hostedControlPlaneNamespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	return d.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Lister().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Get(name)
}

func NewManagementClusterInventory(providerBuilder ManagementClusterProviderBuilder, sessionWorkQueue workqueue.TypedRateLimitingInterface[cache.ObjectName]) *ManagementClusterInventory {
	return &ManagementClusterInventory{
		providers:        make(map[string]*ManagementClusterProvider),
		providerBuilder:  providerBuilder,
		sessionWorkQueue: sessionWorkQueue,
		mutex:            sync.Mutex{},
	}
}

type ManagementClusterInventory struct {
	providers        map[string]*ManagementClusterProvider
	mutex            sync.Mutex
	providerBuilder  ManagementClusterProviderBuilder
	sessionWorkQueue workqueue.TypedRateLimitingInterface[cache.ObjectName]
}

func (i *ManagementClusterInventory) Register(ctx context.Context, resourceId string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if _, ok := i.providers[resourceId]; ok {
		return nil
	}

	klog.InfoS("building management cluster provider", "resourceID", resourceId)
	provider, err := i.providerBuilder(ctx, resourceId)
	if err != nil {
		return fmt.Errorf("failed to create management cluster provider: %w", err)
	}

	klog.InfoS("registering management cluster provider informers with work queue", "resourceID", resourceId)

	// Register CSR informer
	csrInformer := provider.KubeInformers.Certificates().V1().CertificateSigningRequests().Informer()
	if err := registerInformer(csrInformer, keyForOwningSession, i.sessionWorkQueue); err != nil {
		return fmt.Errorf("failed to register CSR informer: %w", err)
	}

	// Register CSR Approval informer
	csrApprovalInformer := provider.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Informer()
	if err := registerInformer(csrApprovalInformer, keyForOwningSession, i.sessionWorkQueue); err != nil {
		return fmt.Errorf("failed to register CSR approval informer: %w", err)
	}

	// Register HostedControlPlane informer
	hcpInformer := provider.HypershiftInformers.Hypershift().V1beta1().HostedControlPlanes().Informer()
	if err := registerInformer(hcpInformer, keyForOwningSession, i.sessionWorkQueue); err != nil {
		return fmt.Errorf("failed to register HCP informer: %w", err)
	}

	klog.InfoS("starting management cluster provider informers", "resourceID", resourceId)
	provider.KubeInformers.Start(provider.stopCh)
	provider.HypershiftInformers.Start(provider.stopCh)

	i.providers[resourceId] = provider
	return nil
}

func (i *ManagementClusterInventory) Unregister(resourceId string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	provider, ok := i.providers[resourceId]
	if !ok {
		return fmt.Errorf("management cluster provider not found: %s", resourceId)
	}

	klog.InfoS("unregistering management cluster provider", "resourceID", resourceId)

	// Stop informers
	close(provider.stopCh)
	provider.HypershiftInformers.Shutdown()
	provider.KubeInformers.Shutdown()

	delete(i.providers, resourceId)
	return nil
}

func (i *ManagementClusterInventory) GetProvider(ctx context.Context, resourceId string, timeout time.Duration) (*ManagementClusterProvider, error) {
	i.mutex.Lock()
	provider, ok := i.providers[resourceId]
	i.mutex.Unlock()

	if !ok {
		return nil, fmt.Errorf("management cluster provider not found: %s", resourceId)
	}

	// Wait for caches to sync with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cachesToSync := []cache.InformerSynced{
		provider.KubeInformers.Certificates().V1().CertificateSigningRequests().Informer().HasSynced,
		provider.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Informer().HasSynced,
		provider.HypershiftInformers.Hypershift().V1beta1().HostedControlPlanes().Informer().HasSynced,
	}

	if !cache.WaitForCacheSync(timeoutCtx.Done(), cachesToSync...) {
		return nil, fmt.Errorf("timeout waiting for caches to sync for management cluster: %s", resourceId)
	}

	return provider, nil
}
