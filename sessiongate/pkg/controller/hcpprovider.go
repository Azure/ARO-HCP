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
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"
	certificatesclientv1alpha1 "github.com/openshift/hypershift/client/clientset/clientset/typed/certificates/v1alpha1"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"

	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

// ManagementClusterQuerier provides read access to management cluster resources via informer listers.
type ManagementClusterQuerier interface {
	GetHostedControlPlane(namespace string) (*hypershiftv1beta1.HostedControlPlane, error)
	GetCSR(name string) (*certificatesv1.CertificateSigningRequest, error)
	GetCSRApproval(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
}

type ManagementClusterProviderFactory struct {
	azureCredentials azcore.TokenCredential
}

func NewManagementClusterProviderFactory(azureCredentials azcore.TokenCredential) *ManagementClusterProviderFactory {
	return &ManagementClusterProviderFactory{
		azureCredentials: azureCredentials,
	}
}

func (f *ManagementClusterProviderFactory) BuildManagementClusterProvider(ctx context.Context, resourceId string) (*ManagementClusterProvider, error) {
	kubeConfig, err := mc.GetAKSRESTConfig(ctx, resourceId, f.azureCredentials)
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

// managementClusterProvider implements ManagementClusterProvider
type ManagementClusterProvider struct {
	HypershiftClient    hypershiftclientset.Interface
	HypershiftInformers hypershiftinformers.SharedInformerFactory
	CertificatesClient  certificatesclientv1alpha1.CertificatesV1alpha1Interface
	KubeClient          kubernetes.Interface
	KubeInformers       kubeinformers.SharedInformerFactory
	stopCh              chan struct{}
}

func (d *ManagementClusterProvider) GetHostedControlPlane(namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
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
		return nil, fmt.Errorf("multiple HostedControlPlanes found for namespace %s", namespace)
	}
	return hcpList[0], nil
}

func (d *ManagementClusterProvider) GetCSR(name string) (*certificatesv1.CertificateSigningRequest, error) {
	return d.KubeInformers.Certificates().V1().CertificateSigningRequests().Lister().Get(name)
}

func (d *ManagementClusterProvider) GetCSRApproval(hostedControlPlaneNamespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	return d.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Lister().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Get(name)
}

// Management cluster provider lifecycle methods on SessionController.
//
// registerMCProvider and unregisterMCProvider are called exclusively from the
// management cluster workqueue worker (single goroutine, see Run()), so they
// never run concurrently with each other. The workqueue provides deduplication
// and serialization, guaranteeing that at most one goroutine is writing to the
// providers map at any time. The write lock exists solely to synchronize with
// concurrent readers (session worker goroutines calling getManagementClusterProvider).
//
// getManagementClusterProvider is called from session worker goroutines and uses
// a read lock for safe concurrent access to the providers map.

func (c *SessionController) registerMCProvider(ctx context.Context, resourceId string, cacheSyncTimeout time.Duration) error {
	c.mcProvidersMu.RLock()
	_, ok := c.mcProviders[resourceId]
	c.mcProvidersMu.RUnlock()
	if ok {
		return nil
	}

	klog.InfoS("building management cluster provider", "resourceID", resourceId)
	provider, err := c.mcProviderFactory.BuildManagementClusterProvider(ctx, resourceId)
	if err != nil {
		return fmt.Errorf("failed to create management cluster provider: %w", err)
	}

	klog.InfoS("registering management cluster provider informers with work queue", "resourceID", resourceId)

	// Register CSR informer
	csrInformer := provider.KubeInformers.Certificates().V1().CertificateSigningRequests().Informer()
	if err := registerInformer(csrInformer, sessionKeyFromOwnershipAnnotation, c.workqueue); err != nil {
		close(provider.stopCh)
		return fmt.Errorf("failed to register CSR informer: %w", err)
	}

	// Register CSR Approval informer
	csrApprovalInformer := provider.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Informer()
	if err := registerInformer(csrApprovalInformer, sessionKeyFromOwnershipAnnotation, c.workqueue); err != nil {
		close(provider.stopCh)
		return fmt.Errorf("failed to register CSR approval informer: %w", err)
	}

	// Register HostedControlPlane informer.
	// Only enqueue sessions on HCP creation/deletion and when the HCP Available condition
	// changes to avoid unnecessary reconciliations from unrelated HCP status updates.
	hcpInformer := provider.HypershiftInformers.Hypershift().V1beta1().HostedControlPlanes().Informer()
	enqueueSessionsForHCP := func(obj interface{}) {
		hcp, ok := obj.(*hypershiftv1beta1.HostedControlPlane)
		if !ok {
			return
		}
		for _, key := range c.sessionKeysForHCP(resourceId, hcp) {
			c.workqueue.Add(key)
		}
	}
	if _, err := hcpInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: enqueueSessionsForHCP,
		UpdateFunc: func(old, cur interface{}) {
			oldHCP, ok := old.(*hypershiftv1beta1.HostedControlPlane)
			if !ok {
				return
			}
			curHCP, ok := cur.(*hypershiftv1beta1.HostedControlPlane)
			if !ok {
				return
			}
			oldAvailable := meta.FindStatusCondition(oldHCP.Status.Conditions, "Available")
			curAvailable := meta.FindStatusCondition(curHCP.Status.Conditions, "Available")
			if !hasConditionStatusChanged(oldAvailable, curAvailable) {
				return
			}
			enqueueSessionsForHCP(curHCP)
		},
		DeleteFunc: enqueueSessionsForHCP,
	}); err != nil {
		return fmt.Errorf("failed to register HCP informer: %w", err)
	}

	klog.InfoS("starting management cluster provider informers", "resourceID", resourceId)
	provider.KubeInformers.Start(provider.stopCh)
	provider.HypershiftInformers.Start(provider.stopCh)

	klog.InfoS("waiting for management cluster provider caches to sync", "resourceID", resourceId)
	timeoutCtx, cancel := context.WithTimeout(ctx, cacheSyncTimeout)
	defer cancel()

	cachesToSync := []cache.InformerSynced{
		provider.KubeInformers.Certificates().V1().CertificateSigningRequests().Informer().HasSynced,
		provider.HypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Informer().HasSynced,
		provider.HypershiftInformers.Hypershift().V1beta1().HostedControlPlanes().Informer().HasSynced,
	}

	if !cache.WaitForCacheSync(timeoutCtx.Done(), cachesToSync...) {
		// close stopCh first: Shutdown() calls wg.Wait() which blocks until
		// all informer goroutines exit, and they only exit when stopCh is closed.
		close(provider.stopCh)
		provider.HypershiftInformers.Shutdown()
		provider.KubeInformers.Shutdown()
		return fmt.Errorf("timeout waiting for caches to sync for management cluster: %s", resourceId)
	}

	c.mcProvidersMu.Lock()
	c.mcProviders[resourceId] = provider
	c.mcProvidersMu.Unlock()

	klog.InfoS("management cluster provider registered", "resourceID", resourceId)

	// Re-queue all sessions for this management cluster now that the
	// provider is registered and caches are synced.
	sessions, err := c.getSessionsByManagementCluster(resourceId)
	if err != nil {
		return fmt.Errorf("failed to re-queue sessions after provider registration: %w", err)
	}
	for _, session := range sessions {
		c.workqueue.Add(cache.ObjectName{
			Namespace: session.Namespace,
			Name:      session.Name,
		})
	}

	return nil
}

func (c *SessionController) unregisterMCProvider(resourceId string) error {
	c.mcProvidersMu.Lock()
	defer c.mcProvidersMu.Unlock()

	provider, ok := c.mcProviders[resourceId]
	if !ok {
		return nil
	}

	klog.InfoS("unregistering management cluster provider", "resourceID", resourceId)

	// close stopCh first: Shutdown() calls wg.Wait() which blocks until
	// all informer goroutines exit, and they only exit when stopCh is closed.
	close(provider.stopCh)
	provider.HypershiftInformers.Shutdown()
	provider.KubeInformers.Shutdown()

	delete(c.mcProviders, resourceId)
	return nil
}

// hasConditionStatusChanged returns true if the condition status
// differs between old and new, including the case where one or both are nil.
func hasConditionStatusChanged(old, cur *metav1.Condition) bool {
	if old == nil && cur == nil {
		return false
	}
	if old == nil || cur == nil {
		return true
	}
	return old.Status != cur.Status
}

func (c *SessionController) getManagementClusterProvider(resourceId string) (*ManagementClusterProvider, bool) {
	c.mcProvidersMu.RLock()
	defer c.mcProvidersMu.RUnlock()

	provider, ok := c.mcProviders[resourceId]
	return provider, ok
}
