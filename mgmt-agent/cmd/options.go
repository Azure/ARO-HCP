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
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
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
	leaderElectionLock       resourcelock.Interface
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

	leKubeConfig := rest.CopyConfig(kubeConfig)
	leKubeConfig.QPS = 20
	leKubeConfig.Burst = 40

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname for leader election: %w", err)
	}

	leaderElectionLock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		o.Namespace,
		LeaderElectionLockName,
		resourcelock.ResourceLockConfig{Identity: hostname},
		leKubeConfig,
		leaderElectionRenewDeadline,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create leader election lock: %w", err)
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
			leaderElectionLock:       leaderElectionLock,
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

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("Run returned"))

	logger := klog.FromContext(ctx)

	// Launch servers and leader election as independent goroutines.
	// Each goroutine sends its result on errCh when done. On first
	// error or context cancellation, cancel propagates to all.
	goroutines := 2 // health server + leader election always run
	errCh := make(chan error, goroutines)

	// health/metrics server
	go func() {
		defer utilruntime.HandleCrash()
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.Handle("/metrics", legacyregistry.Handler())
		server := &http.Server{Addr: o.healthAddress, Handler: mux}
		err := runHTTPServer(ctx, server, "health server")
		if err != nil {
			cancel(fmt.Errorf("health server exited: %w", err))
		}
		errCh <- err
	}()

	// controllers with leader election
	go func() {
		defer utilruntime.HandleCrash()
		err := o.runControllersUnderLeaderElection(ctx)
		// When leader election exits (e.g. lost lease), cancel so Run() unblocks and performs shutdown.
		cancel(fmt.Errorf("leader election exited"))
		errCh <- err
	}()

	<-ctx.Done()

	errs := []error{}
	for range goroutines {
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	logger.Info("mgmt-agent stopped")
	return errors.Join(errs...)
}

const (
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

// runControllersUnderLeaderElection runs the controllers inside the leader-election callback.
// Informers are started inside the callback: a non-leader replica should not be running controllers.
func (o *ControllerOptions) runControllersUnderLeaderElection(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          o.leaderElectionLock,
		LeaseDuration: leaderElectionLeaseDuration,
		RenewDeadline: leaderElectionRenewDeadline,
		RetryPeriod:   leaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("acquired leader election lease; starting informers and controllers")
				o.kubeInformers.Start(ctx.Done())
				if o.hypershiftInformers != nil {
					o.hypershiftInformers.Start(ctx.Done())
				}
				if o.ksmKubeInformers != nil {
					o.ksmKubeInformers.Start(ctx.Done())
				}
				if o.clusterWideKubeInformers != nil {
					o.clusterWideKubeInformers.Start(ctx.Done())
				}
				if o.dynamicInformers != nil {
					o.dynamicInformers.Start(ctx.Done())
				}

				go func() {
					defer utilruntime.HandleCrash()
					if err := o.ctrl.Run(ctx, o.workers); err != nil {
						logger.Error(err, "SwiftNIC controller failed")
					}
				}()
				go func() {
					defer utilruntime.HandleCrash()
					if err := o.resourceWatcher.Run(ctx); err != nil {
						logger.Error(err, "resource watcher failed")
					}
				}()
				if o.ksmCtrl != nil {
					go func() {
						defer utilruntime.HandleCrash()
						if err := o.ksmCtrl.Run(ctx, o.workers); err != nil {
							logger.Error(err, "KSM HCP controller failed")
						}
					}()
				}
				if o.podWatcher != nil {
					go func() {
						defer utilruntime.HandleCrash()
						if err := o.podWatcher.Run(ctx); err != nil {
							logger.Error(err, "pod watcher failed")
						}
					}()
				}

				// Block until the leadership context is cancelled.
				<-ctx.Done()
			},
			OnStoppedLeading: func() {
				logger.Info("lost leader election lease")
			},
		},
		ReleaseOnCancel: true,
		Name:            LeaderElectionLockName,
	})
	if err != nil {
		return err
	}
	le.Run(ctx)
	return nil
}

// runHTTPServer runs the server and shuts it down when ctx is cancelled.
// It returns nil if the server was shut down cleanly (http.ErrServerClosed),
// or the underlying error if ListenAndServe failed for another reason.
func runHTTPServer(ctx context.Context, server *http.Server, name string) error {
	logger := klog.FromContext(ctx)

	done := make(chan struct{})
	defer close(done)
	go func() {
		defer utilruntime.HandleCrash()
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
			defer shutdownCancel()
			logger.Info(fmt.Sprintf("shutting down %s", name))
			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Error(err, fmt.Sprintf("failed to shut down %s", name))
			} else {
				logger.Info(fmt.Sprintf("%s shut down completed", name))
			}
		case <-done:
		}
	}()

	logger.Info(fmt.Sprintf("%s listening on %s", name, server.Addr))
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
