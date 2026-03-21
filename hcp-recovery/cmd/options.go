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
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/ARO-HCP/hcp-recovery/pkg/recovery"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
	"github.com/Azure/ARO-HCP/hcp-recovery/pkg/controller"
	clientset "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/clientset/versioned"
	informers "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/informers/externalversions"
	"github.com/Azure/ARO-HCP/hcp-recovery/pkg/signals"
)

const (
	LeaderElectionLockName = "hcp-recovery-controller-leader"
)

type RawControllerOptions struct {
	BindAddress                 string
	Kubeconfig                  string
	Namespace                   string
	Workers                     int
	LeaderElectionLeaseDuration time.Duration
	LeaderElectionRenewDeadline time.Duration
	LeaderElectionRetryPeriod   time.Duration
}

func DefaultControllerOptions() *RawControllerOptions {
	return &RawControllerOptions{
		BindAddress:                 ":8080",
		LeaderElectionLeaseDuration: 15 * time.Second,
		LeaderElectionRenewDeadline: 10 * time.Second,
		LeaderElectionRetryPeriod:   2 * time.Second,
		Workers:                     5,
	}
}

func (o *RawControllerOptions) BindFlags(cmd *cobra.Command) error {
	// Initialize klog flags before adding them to cobra
	klog.InitFlags(nil)

	cmd.Flags().StringVar(&o.BindAddress, "bind-address", o.BindAddress, "The address the health/readiness server binds to")
	cmd.Flags().StringVar(&o.Kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Optional.")
	cmd.Flags().StringVar(&o.Namespace, "namespace", os.Getenv("POD_NAMESPACE"), "The namespace where the hcp-recovery controller is deployed.")
	cmd.Flags().IntVar(&o.Workers, "workers", o.Workers, "Number of reconcile workers to run")
	cmd.Flags().DurationVar(&o.LeaderElectionLeaseDuration, "leader-election-lease-duration", o.LeaderElectionLeaseDuration, "Leader election lease duration")
	cmd.Flags().DurationVar(&o.LeaderElectionRenewDeadline, "leader-election-renew-deadline", o.LeaderElectionRenewDeadline, "Leader election renew deadline")
	cmd.Flags().DurationVar(&o.LeaderElectionRetryPeriod, "leader-election-retry-period", o.LeaderElectionRetryPeriod, "Leader election retry period")
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	// TODO: Add additional flag bindings specific to hcp-recovery

	return nil
}

type validatedControllerOptions struct {
	*RawControllerOptions
}

type ValidatedControllerOptions struct {
	*validatedControllerOptions
}

type completedControllerOptions struct {
	bindAddress                string
	controller                 *controller.HCPRecoveryController
	hcpRecoveryInformerFactory informers.SharedInformerFactory
	kubeInformerFactory        kubeinformers.SharedInformerFactory
	workers                    int
	leaderElectionCfg          *controller.LeaderElectionConfig
}

type ControllerOptions struct {
	*completedControllerOptions
}

func (o *RawControllerOptions) Validate(ctx context.Context) (*ValidatedControllerOptions, error) {
	if o.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	// TODO: Add additional validation specific to hcp-recovery
	return &ValidatedControllerOptions{
		validatedControllerOptions: &validatedControllerOptions{
			RawControllerOptions: o,
		},
	}, nil
}

func (o *ValidatedControllerOptions) Complete(ctx context.Context) (*ControllerOptions, error) {
	kubeConfig, err := o.buildKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	kubeClientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	drScheme := runtime.NewScheme()
	if err := recovery.AddToScheme(drScheme); err != nil {
		return nil, fmt.Errorf("failed to add DR schemes: %w", err)
	}
	ctrlClient, err := ctrlclient.New(kubeConfig, ctrlclient.Options{Scheme: drScheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}
	hcpRecoveryClientset, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create hcp-recovery clientset: %w", err)
	}
	hcpRecoveryInformers := informers.NewSharedInformerFactoryWithOptions(
		hcpRecoveryClientset,
		time.Second*300,
		informers.WithNamespace(o.Namespace),
	)

	kubeInformers := kubeinformers.NewSharedInformerFactoryWithOptions(
		kubeClientset,
		time.Second*300,
		kubeinformers.WithNamespace(o.Namespace),
		kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = controller.ManagedByLabelSelector()
		}),
	)

	klog.V(4).Info("Successfully built kubeconfig and clientsets")

	// setup leader election config
	leaderElectionCfg := &controller.LeaderElectionConfig{
		LockName:      LeaderElectionLockName,
		LeaseDuration: o.LeaderElectionLeaseDuration,
		RenewDeadline: o.LeaderElectionRenewDeadline,
		RetryPeriod:   o.LeaderElectionRetryPeriod,
		Namespace:     o.Namespace,
		KubeConfig:    kubeConfig,
	}

	// create event scheme (registers both core and CRD types for event recording)
	eventScheme := runtime.NewScheme()
	err = scheme.AddToScheme(eventScheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add scheme to event scheme: %w", err)
	}
	err = hcprecoveryv1alpha1.AddToScheme(eventScheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add hcprecovery scheme to event scheme: %w", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: kubeClientset.CoreV1().Events(""),
	})
	eventRecorder := eventBroadcaster.NewRecorder(eventScheme, corev1.EventSource{
		Component: "hcp-recovery-controller",
	})

	// create controller
	ctrl, err := controller.NewHCPRecoveryController(
		kubeClientset,
		ctrlClient,
		hcpRecoveryClientset,
		hcpRecoveryInformers,
		kubeInformers,
		o.Namespace,
		eventRecorder,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create hcp-recovery controller: %w", err)
	}

	return &ControllerOptions{
		completedControllerOptions: &completedControllerOptions{
			bindAddress:                o.BindAddress,
			hcpRecoveryInformerFactory: hcpRecoveryInformers,
			kubeInformerFactory:        kubeInformers,
			controller:                 ctrl,
			workers:                    o.Workers,
			leaderElectionCfg:          leaderElectionCfg,
		},
	}, nil
}

// buildKubeConfig builds a Kubernetes REST config, trying in-cluster config first
// and falling back to out-of-cluster config using default loading rules.
func (o *ValidatedControllerOptions) buildKubeConfig() (*rest.Config, error) {
	// try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		klog.V(6).Info("Using in-cluster kubeconfig")
		return config, nil
	}

	// fall back to out-of-cluster config
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
	ctx = signals.SetupSignalHandler(ctx)
	logger := klog.FromContext(ctx)

	// start informers
	o.hcpRecoveryInformerFactory.Start(ctx.Done())
	o.kubeInformerFactory.Start(ctx.Done())
	logger.V(6).Info("Informer factories started")

	// use errgroup to run controller concurrently with other components
	// the first component to fail will cancel the context for the others
	g, ctx := errgroup.WithContext(ctx)

	// run health/readiness server
	g.Go(func() error {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		server := &http.Server{
			Addr:              o.bindAddress,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}

		listener, err := net.Listen("tcp", o.bindAddress)
		if err != nil {
			return fmt.Errorf("failed to bind to %s: %w", o.bindAddress, err)
		}
		defer listener.Close()

		logger.Info("Starting health server", "address", o.bindAddress)

		serverErr := make(chan error, 1)
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				serverErr <- err
			}
		}()

		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		case err := <-serverErr:
			return err
		}
	})

	// run controller with leader election
	g.Go(func() error {
		logger.Info("Starting hcp-recovery controller")
		if err := controller.RunWithLeaderElection(ctx, "hcp-recovery", o.leaderElectionCfg, func() error {
			return o.controller.Run(ctx, o.workers)
		}); err != nil {
			logger.Error(err, "HCP recovery controller stopped with error")
			return err
		}
		logger.Info("HCP recovery controller stopped")
		return nil
	})

	if err := g.Wait(); err != nil {
		logger.Error(err, "Component failed")
		klog.Flush()
		return err
	}

	return nil
}
