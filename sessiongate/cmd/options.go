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
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	clientset "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	informers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/server"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/signals"
)

const (
	LeaderElectionLockName = "sessiongate-controller-leader"
)

type RawControllerOptions struct {
	BindAddress                 string
	IngressBaseURL              string
	Kubeconfig                  string
	Namespace                   string
	Workers                     int
	LeaderElectionLeaseDuration time.Duration
	LeaderElectionRenewDeadline time.Duration
	LeaderElectionRetryPeriod   time.Duration
}

func DefaultControllerOptions() *RawControllerOptions {
	return &RawControllerOptions{
		BindAddress:                 "localhost:8080",
		LeaderElectionLeaseDuration: 15 * time.Second,
		LeaderElectionRenewDeadline: 10 * time.Second,
		LeaderElectionRetryPeriod:   2 * time.Second,
		Workers:                     5,
	}
}

func (o *RawControllerOptions) BindFlags(cmd *cobra.Command) error {
	// Initialize klog flags before adding them to cobra
	klog.InitFlags(nil)

	cmd.Flags().StringVar(&o.BindAddress, "bind-address", o.BindAddress, "The local bind address for the HTTP server (e.g., ':8080' or 'localhost:8080')")
	cmd.Flags().StringVar(&o.IngressBaseURL, "ingress-base-url", o.IngressBaseURL, "The externally-accessible base URL for ingress (e.g., 'https://sessiongate.example.com'). If empty, server-address will be used.")
	cmd.Flags().StringVar(&o.Kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Optional.")
	cmd.Flags().StringVar(&o.Namespace, "namespace", os.Getenv("POD_NAMESPACE"), "The namespace where the sessiongate controller is deployed.")
	cmd.Flags().IntVar(&o.Workers, "workers", o.Workers, "Number of reconcile workers to run")
	cmd.Flags().DurationVar(&o.LeaderElectionLeaseDuration, "leader-election-lease-duration", o.LeaderElectionLeaseDuration, "Leader election lease duration")
	cmd.Flags().DurationVar(&o.LeaderElectionRenewDeadline, "leader-election-renew-deadline", o.LeaderElectionRenewDeadline, "Leader election renew deadline")
	cmd.Flags().DurationVar(&o.LeaderElectionRetryPeriod, "leader-election-retry-period", o.LeaderElectionRetryPeriod, "Leader election retry period")
	cmd.Flags().AddGoFlagSet(flag.CommandLine)

	return nil
}

type validatedControllerOptions struct {
	*RawControllerOptions
}

type ValidatedControllerOptions struct {
	*validatedControllerOptions
}

type completedControllerOptions struct {
	server                     *server.Server
	controlPlaneController     *controller.SessionController
	dataPlaneController        *controller.DataplaneController
	sessiongateInformerFactory informers.SharedInformerFactory
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
	if o.BindAddress == "" {
		return nil, fmt.Errorf("bind-address is required")
	}
	if o.IngressBaseURL == "" {
		return nil, fmt.Errorf("ingress-base-url is required")
	}
	return &ValidatedControllerOptions{
		validatedControllerOptions: &validatedControllerOptions{
			RawControllerOptions: o,
		},
	}, nil
}

func (o *ValidatedControllerOptions) Complete(ctx context.Context) (*ControllerOptions, error) {
	logger := klog.FromContext(ctx)

	azureCredential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
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
	sessiongateClientset, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create sessiongate clientset: %w", err)
	}
	sessiongateInformers := informers.NewSharedInformerFactoryWithOptions(
		sessiongateClientset,
		time.Second*300,
		informers.WithNamespace(o.Namespace),
	)

	// create Secret informer for watching session credentials
	kubeInformers := kubeinformers.NewSharedInformerFactoryWithOptions(
		kubeClientset,
		time.Second*300,
		kubeinformers.WithNamespace(o.Namespace),
		kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = controller.ManagedByLabelSelector()
		}),
	)

	klog.V(4).Info("Successfully built kubeconfig and clientsets")

	// create server
	srv := server.NewServer(o.BindAddress, o.IngressBaseURL, prometheus.DefaultRegisterer)

	// setup leader election config
	leaderElectionCfg := &controller.LeaderElectionConfig{
		LockName:      LeaderElectionLockName,
		LeaseDuration: o.LeaderElectionLeaseDuration,
		RenewDeadline: o.LeaderElectionRenewDeadline,
		RetryPeriod:   o.LeaderElectionRetryPeriod,
		Namespace:     o.Namespace,
		KubeConfig:    kubeConfig,
	}

	// create event recorders
	eventScheme := runtime.NewScheme()
	err = scheme.AddToScheme(eventScheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add scheme to event scheme: %w", err)
	}
	err = sessiongatev1alpha1.AddToScheme(eventScheme)
	if err != nil {
		return nil, fmt.Errorf("failed to add scheme to event scheme: %w", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: kubeClientset.CoreV1().Events(""),
	})
	controlPlaneEventRecorder := eventBroadcaster.NewRecorder(eventScheme, corev1.EventSource{
		Component: "sessiongate-control-plane",
	})
	dataPlaneEventRecorder := eventBroadcaster.NewRecorder(eventScheme, corev1.EventSource{
		Component: "sessiongate-data-plane",
	})

	// create control plane controller (leader-elected)
	controlPlaneCtrl, err := controller.NewSessionController(
		kubeClientset,
		sessiongateClientset,
		sessiongateInformers,
		kubeInformers,
		controlPlaneEventRecorder,
		o.Namespace,
		controller.NewManagementClusterProviderFactory(azureCredential),
		srv,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create control plane controller: %w", err)
	}

	// create data plane controller (no leader election, runs on all replicas)
	dataPlaneCtrl, err := controller.NewDataplaneController(
		ctx,
		klog.LoggerWithValues(logger, "controller", "data-plane"),
		sessiongateInformers,
		kubeInformers,
		srv,
		dataPlaneEventRecorder,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create data plane controller: %w", err)
	}

	return &ControllerOptions{
		completedControllerOptions: &completedControllerOptions{
			server:                     srv,
			sessiongateInformerFactory: sessiongateInformers,
			kubeInformerFactory:        kubeInformers,
			controlPlaneController:     controlPlaneCtrl,
			dataPlaneController:        dataPlaneCtrl,
			workers:                    o.Workers,
			leaderElectionCfg:          leaderElectionCfg,
		},
	}, nil
}

// build a Kubernetes REST config, trying in-cluster config first
// and falling back to out-of-cluster config using default loading rules
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
	o.sessiongateInformerFactory.Start(ctx.Done())
	o.kubeInformerFactory.Start(ctx.Done())
	logger.V(6).Info("Informer factories started")

	// use errgroup to run server and controller concurrently
	// the first component to fail will cancel the context for the other
	g, ctx := errgroup.WithContext(ctx)

	// run webserver
	g.Go(func() error {
		logger.Info("Starting webserver", "address", o.server.BindAddress())
		if err := o.server.Run(ctx); err != nil {
			logger.Error(err, "Webserver stopped with error")
			return err
		}
		logger.Info("Webserver stopped")
		return nil
	})

	// run control plane controller
	g.Go(func() error {
		logger.Info("Starting control plane controller")
		if err := controller.RunWithLeaderElection(ctx, "controlplane", o.leaderElectionCfg, func() error {
			return o.controlPlaneController.Run(ctx, o.workers)
		}); err != nil {
			logger.Error(err, "Control plane controller stopped with error")
			return err
		}
		logger.Info("Control plane controller stopped")
		return nil
	})

	// run data plane controller
	g.Go(func() error {
		logger.Info("Starting data plane controller")
		if err := o.dataPlaneController.Run(ctx, o.workers); err != nil {
			logger.Error(err, "Data plane controller stopped with error")
			return err
		}
		logger.Info("Data plane controller stopped")
		return nil
	})

	if err := g.Wait(); err != nil {
		logger.Error(err, "Component failed")
		klog.Flush()
		return err
	}

	return nil
}
