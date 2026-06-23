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

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/ksmhcp"
)

const (
	LeaderElectionLockName = "mgmt-agent-controller-leader"
)

type RawControllerOptions struct {
	HealthAddress string
	Kubeconfig    string
	Namespace     string
	Workers       int
	LogVerbosity  int
	KSMImage      string
}

func DefaultControllerOptions() *RawControllerOptions {
	return &RawControllerOptions{
		HealthAddress: ":8080",
		Workers:       2,
	}
}

func (o *RawControllerOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.HealthAddress, "health-address", o.HealthAddress, "The bind address for the health check server (e.g., ':8080')")
	cmd.Flags().StringVar(&o.Kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Optional.")
	cmd.Flags().StringVar(&o.Namespace, "namespace", os.Getenv("POD_NAMESPACE"), "The namespace where the mgmt-agent controller is deployed.")
	cmd.Flags().IntVar(&o.Workers, "workers", o.Workers, "Number of reconcile workers to run")
	cmd.Flags().IntVar(&o.LogVerbosity, "log-verbosity", o.LogVerbosity,
		"Log verbosity. 0 is the default verbosity level, equivalent to INFO. "+
			"It must be a value >= 0, where a higher value means more verbose output.")
	cmd.Flags().StringVar(&o.KSMImage, "ksm-image", o.KSMImage, "Container image for kube-state-metrics deployed per HCP namespace")

	return nil
}

type validatedControllerOptions struct {
	*RawControllerOptions
}

type ValidatedControllerOptions struct {
	*validatedControllerOptions
}

type completedControllerOptions struct {
	ctrl                     *controller.SwiftNICController
	ksmCtrl                  *ksmhcp.KSMHCPController
	resourceWatcher          *controller.ResourceWatcher
	podWatcher               *controller.PodWatcher
	kubeInformers            kubeinformers.SharedInformerFactory
	ksmKubeInformers         kubeinformers.SharedInformerFactory
	clusterWideKubeInformers kubeinformers.SharedInformerFactory
	hypershiftInformers      hypershiftinformers.SharedInformerFactory
	dynamicInformers         dynamicinformer.DynamicSharedInformerFactory
	workers                  int
	healthAddress            string
	leaderElectionCfg        *controller.LeaderElectionConfig
}

type ControllerOptions struct {
	*completedControllerOptions
}

func (o *RawControllerOptions) Validate(ctx context.Context) (*ValidatedControllerOptions, error) {
	if o.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if o.HealthAddress == "" {
		return nil, fmt.Errorf("health-address is required")
	}
	if o.LogVerbosity < 0 {
		return nil, fmt.Errorf("--log-verbosity must be a value >= 0")
	}
	return &ValidatedControllerOptions{
		validatedControllerOptions: &validatedControllerOptions{
			RawControllerOptions: o,
		},
	}, nil
}

func (o *ValidatedControllerOptions) Complete(ctx context.Context) (*ControllerOptions, error) {
	azureCredential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	kubeConfig, err := o.buildKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	kubeClientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	kubeInformers := kubeinformers.NewSharedInformerFactory(kubeClientset, 10*time.Minute)

	ctrl, err := controller.NewSwiftNICController(
		kubeClientset,
		kubeInformers.Core().V1().Nodes(),
		azureCredential,
		nil, // use default Azure SDK implementation
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create controller: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	resourceWatcher := controller.NewResourceWatcher(dynamicClient, discoveryClient)

	var ksmCtrl *ksmhcp.KSMHCPController
	var podWatcher *controller.PodWatcher
	var hsInformers hypershiftinformers.SharedInformerFactory
	var ksmKubeInformers kubeinformers.SharedInformerFactory
	var clusterWideKubeInformers kubeinformers.SharedInformerFactory
	var dynInformers dynamicinformer.DynamicSharedInformerFactory
	if o.KSMImage != "" {
		hsClient, err := hypershiftclient.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create hypershift clientset: %w", err)
		}
		hsInformers = hypershiftinformers.NewSharedInformerFactory(hsClient, 10*time.Minute)
		ksmKubeInformers = kubeinformers.NewSharedInformerFactoryWithOptions(kubeClientset, 10*time.Minute,
			kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.LabelSelector = ksmhcp.LabelSelector
			}),
		)
		dynInformers = dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 10*time.Minute, metav1.NamespaceAll,
			func(opts *metav1.ListOptions) {
				opts.LabelSelector = ksmhcp.LabelSelector
			},
		)

		ksmCtrl, err = ksmhcp.NewKSMHCPController(
			kubeClientset,
			dynamicClient,
			hsInformers.Hypershift().V1beta1().HostedControlPlanes(),
			ksmKubeInformers.Apps().V1().Deployments().Informer(),
			ksmKubeInformers.Core().V1().Services().Informer(),
			dynInformers.ForResource(ksmhcp.ServiceMonitorGVR).Informer(),
			o.KSMImage,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create KSM HCP controller: %w", err)
		}

		clusterWideKubeInformers = kubeinformers.NewSharedInformerFactory(kubeClientset, 0)

		podWatcher, err = controller.NewPodWatcher(clusterWideKubeInformers.Core().V1().Pods())
		if err != nil {
			return nil, fmt.Errorf("failed to create pod watcher: %w", err)
		}
	}

	leaderElectionCfg := &controller.LeaderElectionConfig{
		LockName:      LeaderElectionLockName,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Namespace:     o.Namespace,
		KubeConfig:    kubeConfig,
	}

	return &ControllerOptions{
		completedControllerOptions: &completedControllerOptions{
			ctrl:                     ctrl,
			ksmCtrl:                  ksmCtrl,
			resourceWatcher:          resourceWatcher,
			podWatcher:               podWatcher,
			kubeInformers:            kubeInformers,
			ksmKubeInformers:         ksmKubeInformers,
			clusterWideKubeInformers: clusterWideKubeInformers,
			hypershiftInformers:      hsInformers,
			dynamicInformers:         dynInformers,
			workers:                  o.Workers,
			healthAddress:            o.HealthAddress,
			leaderElectionCfg:        leaderElectionCfg,
		},
	}, nil
}

func (o *ValidatedControllerOptions) buildKubeConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err == nil {
		klog.V(6).Info("Using in-cluster kubeconfig")
		return config, nil
	}

	klog.V(6).Info("Not running in-cluster, using out-of-cluster kubeconfig")
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if o.Kubeconfig != "" {
		loadingRules.ExplicitPath = o.Kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	config, err = kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig from loading rules: %w", err)
	}

	return config, nil
}

func (o *ControllerOptions) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := klog.FromContext(ctx)

	g, ctx := errgroup.WithContext(ctx)

	// health server
	g.Go(func() error {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.Handle("/metrics", legacyregistry.Handler())
		server := &http.Server{Addr: o.healthAddress, Handler: mux}
		go func() {
			defer utilruntime.HandleCrash()
			<-ctx.Done()
			if err := server.Shutdown(context.Background()); err != nil {
				logger.Error(err, "Error shutting down health server")
			}
		}()
		logger.Info("Starting health server", "address", o.healthAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("health server error: %w", err)
		}
		return nil
	})

	// controllers with leader election
	g.Go(func() error {
		logger.Info("Starting controllers under leader election")
		if err := controller.RunWithLeaderElection(ctx, "mgmt-agent", o.leaderElectionCfg, func(leaderCtx context.Context) error {
			o.kubeInformers.Start(leaderCtx.Done())
			if o.hypershiftInformers != nil {
				o.hypershiftInformers.Start(leaderCtx.Done())
			}
			if o.ksmKubeInformers != nil {
				o.ksmKubeInformers.Start(leaderCtx.Done())
			}
			if o.clusterWideKubeInformers != nil {
				o.clusterWideKubeInformers.Start(leaderCtx.Done())
			}
			if o.dynamicInformers != nil {
				o.dynamicInformers.Start(leaderCtx.Done())
			}
			logger.V(6).Info("Informer factories started")

			innerG, innerCtx := errgroup.WithContext(leaderCtx)

			innerG.Go(func() error {
				return o.ctrl.Run(innerCtx, o.workers)
			})

			innerG.Go(func() error {
				return o.resourceWatcher.Run(innerCtx)
			})

			if o.ksmCtrl != nil {
				innerG.Go(func() error {
					return o.ksmCtrl.Run(innerCtx, o.workers)
				})
			}

			if o.podWatcher != nil {
				innerG.Go(func() error {
					return o.podWatcher.Run(innerCtx)
				})
			}

			return innerG.Wait()
		}); err != nil {
			logger.Error(err, "Controllers stopped with error")
			return err
		}
		logger.Info("Controllers stopped")
		return nil
	})

	if err := g.Wait(); err != nil {
		logger.Error(err, "Component failed")
		klog.Flush()
		return err
	}

	return nil
}
