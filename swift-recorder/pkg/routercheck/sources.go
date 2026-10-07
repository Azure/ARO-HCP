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

package routercheck

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	discoveryinformers "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // Deployed CAPI resources use v1beta1.

	routev1 "github.com/openshift/api/route/v1"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

const ignitionCAName = "ignition-server-ca-cert"
const rootCAName = "root-ca"
const maxTrustBundleBytes = 64 * 1024

// discoverySources uses cluster-wide informers with resource-specific filters
// and namespace indexes for per-HCP reads.
type discoverySources struct {
	factories        []informers.SharedInformerFactory
	dynamicFactories []dynamicinformer.DynamicSharedInformerFactory
	pods             coreinformers.PodInformer
	services         coreinformers.ServiceInformer
	slices           discoveryinformers.EndpointSliceInformer
	secrets          coreinformers.SecretInformer
	configMaps       coreinformers.ConfigMapInformer
	objects          map[schema.GroupVersionResource]informers.GenericInformer
}

func newDiscoverySources(client kubernetes.Interface, dynamicClient dynamic.Interface) *discoverySources {
	podFactory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.LabelSelector = "app in (private-router,ignition-server,ignition-server-proxy,kube-apiserver)"
	}))
	// The ignition-server-proxy Service has no component label.
	serviceFactory := informers.NewSharedInformerFactory(client, 0)
	sliceFactory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.LabelSelector = discoveryv1.LabelServiceName + " in (ignition-server,ignition-server-proxy,kube-apiserver)"
	}))
	secretFactory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", ignitionCAName).String()
	}))
	configMapFactory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", rootCAName).String()
	}))
	s := &discoverySources{
		factories: []informers.SharedInformerFactory{podFactory, serviceFactory, sliceFactory, secretFactory, configMapFactory},
		pods:      podFactory.Core().V1().Pods(), services: serviceFactory.Core().V1().Services(), slices: sliceFactory.Discovery().V1().EndpointSlices(),
		secrets: secretFactory.Core().V1().Secrets(), configMaps: configMapFactory.Core().V1().ConfigMaps(),
		objects: map[schema.GroupVersionResource]informers.GenericInformer{},
	}
	s.pods.Informer()
	s.services.Informer()
	s.slices.Informer()
	s.secrets.Informer()
	s.configMaps.Informer()
	for _, resource := range []struct {
		gvr      schema.GroupVersionResource
		selector string
	}{
		{routeGVR, "hypershift.openshift.io/hosted-control-plane"},
		{mtpncGVR, ""},
		{machineGVR, clusterNameLabel},
		{azureMachineGVR, clusterNameLabel},
	} {
		factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 0, metav1.NamespaceAll, func(options *metav1.ListOptions) {
			options.LabelSelector = resource.selector
		})
		s.dynamicFactories = append(s.dynamicFactories, factory)
		s.objects[resource.gvr] = factory.ForResource(resource.gvr)
	}
	return s
}

func (s *discoverySources) Start(stop <-chan struct{}) {
	for _, factory := range s.factories {
		factory.Start(stop)
	}
	for _, factory := range s.dynamicFactories {
		factory.Start(stop)
	}
}

func (s *discoverySources) Shutdown() {
	for _, factory := range s.factories {
		factory.Shutdown()
	}
	for _, factory := range s.dynamicFactories {
		factory.Shutdown()
	}
}

func (s *discoverySources) HasSynced() []cache.InformerSynced {
	checks := []cache.InformerSynced{s.pods.Informer().HasSynced, s.services.Informer().HasSynced, s.slices.Informer().HasSynced, s.secrets.Informer().HasSynced, s.configMaps.Informer().HasSynced}
	for _, gvr := range []schema.GroupVersionResource{routeGVR, mtpncGVR, machineGVR, azureMachineGVR} {
		checks = append(checks, s.objects[gvr].Informer().HasSynced)
	}
	return checks
}

func cachedResource[T any](informer informers.GenericInformer, namespace, name string) (*T, error) {
	object, err := informer.Lister().ByNamespace(namespace).Get(name)
	if err != nil {
		return nil, err
	}
	var result T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.(*unstructured.Unstructured).Object, &result); err != nil {
		return nil, fmt.Errorf("decode %s/%s: %w", namespace, name, err)
	}
	return &result, nil
}

// Gather reads informer listers after controller startup synchronization.
// Every required lookup failure aborts the pass.
func Gather(ctx context.Context, sources *discoverySources, pod *corev1.Pod, dns probe.DNSConfig) (DiscoveryInput, error) {
	if err := ctx.Err(); err != nil {
		return DiscoveryInput{}, err
	}
	input := DiscoveryInput{Pod: pod, DNS: dns}
	var err error
	input.IgnitionRoute, err = cachedResource[routev1.Route](sources.objects[routeGVR], pod.Namespace, "ignition-server")
	if err != nil {
		return DiscoveryInput{}, err
	}
	for _, name := range []string{"kube-apiserver-internal", "kube-apiserver", "kube-apiserver-private"} {
		route, err := cachedResource[routev1.Route](sources.objects[routeGVR], pod.Namespace, name)
		if apierrors.IsNotFound(err) {
			continue // KAS routes represent alternative publishing strategies.
		}
		if err != nil {
			return DiscoveryInput{}, err
		}
		input.KASRoutes = append(input.KASRoutes, route)
	}
	if len(input.KASRoutes) == 0 {
		return DiscoveryInput{}, apierrors.NewNotFound(routeGVR.GroupResource(), "kube-apiserver routes")
	}
	input.LocalMTPNC, err = cachedResource[MTPNC](sources.objects[mtpncGVR], pod.Namespace, pod.Name)
	if err != nil {
		return DiscoveryInput{}, err
	}
	peers, err := sources.pods.Lister().Pods(pod.Namespace).List(labels.SelectorFromSet(labels.Set{"app": "private-router", pniLabel: pod.Labels[pniLabel]}))
	if err != nil {
		return DiscoveryInput{}, err
	}
	slices.SortFunc(peers, func(a, b *corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })
	for _, peer := range peers {
		if peer.UID == pod.UID {
			continue
		}
		mtpnc, err := cachedResource[MTPNC](sources.objects[mtpncGVR], pod.Namespace, peer.Name)
		if err != nil {
			return DiscoveryInput{}, err
		}
		input.Peers = append(input.Peers, RouterInput{Pod: peer, MTPNC: mtpnc})
	}
	for _, name := range []string{"ignition-server", "ignition-server-proxy", "kube-apiserver"} {
		svc, err := sources.services.Lister().Services(pod.Namespace).Get(name)
		if err != nil {
			return DiscoveryInput{}, err
		}
		if len(svc.Spec.Selector) == 0 {
			return DiscoveryInput{}, fmt.Errorf("service %s has no selector: %w", name, ErrDiscoveryData)
		}
		pods, err := sources.pods.Lister().Pods(pod.Namespace).List(labels.SelectorFromSet(svc.Spec.Selector))
		if err != nil {
			return DiscoveryInput{}, err
		}
		slices.SortFunc(pods, func(a, b *corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })
		endpoints, err := sources.slices.Lister().EndpointSlices(pod.Namespace).List(labels.SelectorFromSet(labels.Set{discoveryv1.LabelServiceName: name}))
		if err != nil {
			return DiscoveryInput{}, err
		}
		slices.SortFunc(endpoints, func(a, b *discoveryv1.EndpointSlice) int { return cmp.Compare(a.Name, b.Name) })
		input.Services = append(input.Services, ServiceInput{Service: svc, Pods: pods, Slices: endpoints})
	}
	secret, err := sources.secrets.Lister().Secrets(pod.Namespace).Get(ignitionCAName)
	if err != nil {
		return DiscoveryInput{}, err
	}
	configMap, err := sources.configMaps.Lister().ConfigMaps(pod.Namespace).Get(rootCAName)
	if err != nil {
		return DiscoveryInput{}, err
	}
	input.TrustBundles = map[string]string{probe.TrustIgnition: string(secret.Data[corev1.TLSCertKey]), probe.TrustRoot: configMap.Data["ca.crt"]}
	// Cluster labels scope the inventory; InfrastructureRef identifies each AzureMachine.
	clusterLabel, err := labels.NewRequirement(clusterNameLabel, selection.Exists, nil)
	if err != nil {
		return DiscoveryInput{}, err
	}
	machines, err := sources.objects[machineGVR].Lister().ByNamespace(pod.Namespace).List(labels.NewSelector().Add(*clusterLabel))
	if err != nil {
		return DiscoveryInput{}, err
	}
	if len(machines) == 0 {
		return DiscoveryInput{}, fmt.Errorf("workers empty: %w", ErrDiscoveryData)
	}
	var cluster string
	for _, object := range machines {
		var machine clusterv1.Machine
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.(*unstructured.Unstructured).Object, &machine); err != nil {
			return DiscoveryInput{}, fmt.Errorf("decode Machine: %w", err)
		}
		if machine.Spec.ClusterName == "" || machine.Labels[clusterNameLabel] != machine.Spec.ClusterName || (cluster != "" && cluster != machine.Spec.ClusterName) {
			return DiscoveryInput{}, fmt.Errorf("machine %s cluster identity: %w", machine.Name, ErrDiscoveryData)
		}
		cluster = machine.Spec.ClusterName
		ref := machine.Spec.InfrastructureRef
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil || gv.Group != azureMachineGVR.Group || ref.Kind != "AzureMachine" || ref.Name == "" || (ref.Namespace != "" && ref.Namespace != pod.Namespace) {
			return DiscoveryInput{}, fmt.Errorf("machine %s infrastructure reference: %w", machine.Name, ErrDiscoveryData)
		}
		azure, err := cachedResource[capzv1.AzureMachine](sources.objects[azureMachineGVR], pod.Namespace, ref.Name)
		if err != nil {
			return DiscoveryInput{}, err
		}
		if azure.Labels[clusterNameLabel] != cluster || (ref.UID != "" && ref.UID != azure.UID) {
			return DiscoveryInput{}, fmt.Errorf("azure machine %s identity: %w", azure.Name, ErrDiscoveryData)
		}
		for _, owner := range azure.OwnerReferences {
			if owner.Kind == "Machine" && owner.UID != machine.UID {
				return DiscoveryInput{}, fmt.Errorf("azure machine %s owner: %w", azure.Name, ErrDiscoveryData)
			}
		}
		input.Workers = append(input.Workers, WorkerInput{Machine: &machine, AzureMachine: azure})
	}
	slices.SortFunc(input.Workers, func(a, b WorkerInput) int { return cmp.Compare(a.Machine.Name, b.Machine.Name) })
	return input, nil
}
