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

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/net/netutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/discovery"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/recorder"
)

type RawOptions struct {
	recorder.Config
	HealthAddress string
	Kubeconfig    string
	CNILog        string
	BootIDFile    string
	LogVerbosity  int
}

type ValidatedOptions struct{ options RawOptions }
type CompletedOptions struct {
	options    RawOptions
	factory    informers.SharedInformerFactory
	controller *recorder.Controller
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{Use: "swift-recorder", SilenceUsage: true, SilenceErrors: true}
	o := &RawOptions{Config: recorder.Config{
		NodeName: os.Getenv("NODE_NAME"), CaptureMode: "slow", StartupDwell: 30 * time.Second,
		PostSuccessCapture: 10 * time.Second, SampleInterval: time.Second, CaptureTimeout: 500 * time.Millisecond,
		EpisodeTimeout: 15 * time.Minute, MaxPods: 32, MaxBufferBytes: 16 * 1024 * 1024, MaxRecordBytes: 64 * 1024,
		NetNSDir: "/var/run/netns",
	}, HealthAddress: ":8091", CNILog: "/host/var/log/azure-vnet.log", BootIDFile: "/host/boot-id"}
	controller := &cobra.Command{Use: "controller", Short: "Record live namespace state during SWIFT startup", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			validated, err := o.Validate()
			if err != nil {
				return err
			}
			logger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.Level(-o.LogVerbosity), AddSource: true}))
			klog.SetLogger(logger)
			ctx, stop := signal.NotifyContext(utils.ContextWithLogger(command.Context(), logger), os.Interrupt, syscall.SIGTERM)
			defer stop()
			completed, err := validated.Complete(ctx)
			if err != nil {
				return err
			}
			return completed.Run(ctx)
		}}
	f := controller.Flags()
	f.StringVar(&o.NodeName, "node-name", o.NodeName, "This node's Kubernetes name")
	f.StringVar(&o.ClusterName, "cluster-name", "", "Management cluster name")
	f.StringVar(&o.Region, "region", "", "Azure region")
	f.StringVar(&o.Environment, "environment", "", "Deployment environment")
	f.StringVar(&o.HealthAddress, "health-address", o.HealthAddress, "Health and metrics bind address")
	f.StringVar(&o.Kubeconfig, "kubeconfig", "", "Optional local kubeconfig")
	f.StringVar(&o.CNILog, "cni-log", o.CNILog, "Local active Azure VNet log file")
	f.StringVar(&o.NetNSDir, "netns-dir", o.NetNSDir, "Mounted host namespace directory")
	f.StringVar(&o.BootIDFile, "boot-id-file", o.BootIDFile, "Mounted host boot ID file")
	f.StringVar(&o.CaptureMode, "capture-mode", o.CaptureMode, "slow buffers healthy starts; all publishes all captured startups")
	f.DurationVar(&o.StartupDwell, "startup-dwell", o.StartupDwell, "Unresolved startup age before publication")
	f.DurationVar(&o.PostSuccessCapture, "post-success-capture", o.PostSuccessCapture, "Capture window after sandbox success")
	f.DurationVar(&o.SampleInterval, "sample-interval", o.SampleInterval, "Per-pod state sampling interval")
	f.DurationVar(&o.CaptureTimeout, "capture-timeout", o.CaptureTimeout, "Hard helper process deadline")
	f.DurationVar(&o.EpisodeTimeout, "episode-timeout", o.EpisodeTimeout, "Maximum episode duration")
	f.IntVar(&o.MaxPods, "max-pods", o.MaxPods, "Maximum concurrent startup episodes")
	f.IntVar(&o.MaxBufferBytes, "max-buffer-bytes", o.MaxBufferBytes, "Node-wide encoded observation buffer budget")
	f.IntVar(&o.MaxRecordBytes, "max-record-bytes", o.MaxRecordBytes, "Maximum encoded capture record")
	f.IntVar(&o.LogVerbosity, "log-verbosity", 0, "Nonnegative log verbosity")
	root.AddCommand(controller)
	var namespacePath string
	helper := &cobra.Command{Use: "capture", Hidden: true, Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return capture.Run(namespacePath, os.Stdout) }}
	helper.Flags().StringVar(&namespacePath, "path", "", "Network namespace path")
	root.AddCommand(helper)
	return root
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.NodeName == "" || o.ClusterName == "" || o.Region == "" || o.Environment == "" {
		return nil, fmt.Errorf("node-name, cluster-name, region and environment are required")
	}
	if o.CaptureMode != "slow" && o.CaptureMode != "all" {
		return nil, fmt.Errorf("capture-mode must be slow or all")
	}
	if o.StartupDwell <= 0 || o.PostSuccessCapture < 0 || o.SampleInterval < 100*time.Millisecond || o.CaptureTimeout <= 0 || o.CaptureTimeout > 2*time.Second || o.EpisodeTimeout <= o.PostSuccessCapture {
		return nil, fmt.Errorf("invalid capture timing budgets")
	}
	if o.MaxPods < 1 || o.MaxPods > 256 || o.MaxRecordBytes < 1024 || o.MaxRecordBytes > 1024*1024 || o.MaxBufferBytes < o.MaxRecordBytes*o.MaxPods || o.MaxBufferBytes > 256*1024*1024 {
		return nil, fmt.Errorf("invalid capture memory budgets")
	}
	if o.LogVerbosity < 0 || o.HealthAddress == "" {
		return nil, fmt.Errorf("invalid log verbosity or health address")
	}
	for _, path := range []string{o.CNILog, o.NetNSDir, o.BootIDFile} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("host paths must be absolute")
		}
	}
	return &ValidatedOptions{options: *o}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	options := o.options
	boot, err := os.ReadFile(options.BootIDFile)
	if err != nil {
		return nil, fmt.Errorf("read host boot ID: %w", err)
	}
	options.BootID = strings.TrimSpace(string(boot))
	if options.BootID == "" {
		return nil, fmt.Errorf("empty host boot ID")
	}
	options.NetNSDir, err = filepath.EvalSymlinks(options.NetNSDir)
	if err != nil {
		return nil, fmt.Errorf("resolve mounted namespace directory: %w", err)
	}
	options.Executable, err = os.Executable()
	if err != nil {
		return nil, err
	}
	var config *rest.Config
	if options.Kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", options.Kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes config: %w", err)
	}
	config.UserAgent = "swift-recorder"
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	factory := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(func(opts *metav1.ListOptions) { opts.FieldSelector = "spec.nodeName=" + options.NodeName }))
	ctrl, err := recorder.New(options.Config, factory.Core().V1().Pods(), capture.Snapshot)
	if err != nil {
		return nil, err
	}
	return &CompletedOptions{options: options, factory: factory, controller: ctrl}, nil
}

func (o *CompletedOptions) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", o.options.HealthAddress)
	if err != nil {
		// New allocates a workqueue even before the controller starts.
		cancel()
		_ = o.controller.Run(ctx)
		return err
	}
	var tailReady atomic.Bool
	mux := newMux(func() bool { return tailReady.Load() && o.controller.Ready() })
	// This is a host-network listener: bound both connection count and lifetime.
	listener = netutil.LimitListener(listener, 64)
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 * 1024,
	}
	errorsCh := make(chan error, 3)
	var wg sync.WaitGroup
	start := func(run func() error) {
		wg.Add(1)
		go func() { defer utilruntime.HandleCrash(); defer wg.Done(); err := run(); errorsCh <- err; cancel() }()
	}
	o.factory.Start(ctx.Done())
	start(func() error { return o.controller.Run(ctx) })
	start(func() error {
		return discovery.Tail(ctx, o.options.CNILog, o.controller.ObserveAttempt, func() { tailReady.Store(true) })
	})
	start(func() error { return server.Serve(listener) })
	<-ctx.Done()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
	}
	wg.Wait()
	o.factory.Shutdown()
	close(errorsCh)
	var errs []error
	for err := range errorsCh {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, shutdownErr)...)
}

func newMux(ready func() bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			http.Error(w, "initializing", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/metrics", legacyregistry.Handler())
	return mux
}
