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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	interval := flag.Duration("reconcile-interval", time.Minute, "interval between namespace RBAC reconciliation attempts")
	healthAddress := flag.String("health-address", ":8081", "address for health, readiness, and metrics endpoints")
	exporterNamespace := flag.String("exporter-namespace", "", "namespace containing the cert-exporter service account")
	exporterServiceAccount := flag.String("exporter-service-account", "cert-exporter", "cert-exporter service account name")
	targetNamespacePrefix := flag.String("target-namespace-prefix", "", "prefix of namespaces containing control plane certificates")
	targetNamespaceExact := flag.String("target-namespace", "", "exact namespace containing policy certificates")
	flag.Parse()

	if *exporterNamespace == "" || *targetNamespacePrefix == "" || *targetNamespaceExact == "" {
		slog.Error("--exporter-namespace, --target-namespace-prefix, and --target-namespace are required")
		os.Exit(1)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		slog.Error("failed to create in-cluster configuration", "error", err)
		os.Exit(1)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		slog.Error("failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	controller := newRBACController(client, *exporterNamespace, *exporterServiceAccount, *targetNamespacePrefix, *targetNamespaceExact)
	go serveStatus(*healthAddress, controller.status, 3*(*interval))
	if err := run(ctx, controller, *interval); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("RBAC controller failed", "error", err)
		os.Exit(1)
	}
}

type reconciler interface {
	reconcile(context.Context) error
}

func run(ctx context.Context, controller reconciler, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("reconcile interval must be positive")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		err := controller.reconcile(ctx)
		if observer, ok := controller.(interface{ recordReconcile(bool) }); ok {
			observer.recordReconcile(err == nil)
		}
		if err != nil {
			slog.ErrorContext(ctx, "RBAC reconciliation failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *rbacController) recordReconcile(success bool) {
	c.status.recordReconcile(success)
}

func serveStatus(address string, status *controllerStatus, staleAfter time.Duration) {
	defer utilruntime.HandleCrash()

	mux := http.NewServeMux()
	status.registerHandlers(mux, staleAfter)
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("status server failed", "error", err)
	}
}

func listNamespaces(ctx context.Context, client kubernetes.Interface) ([]string, error) {
	namespaceList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	names := make([]string, 0, len(namespaceList.Items))
	for _, namespace := range namespaceList.Items {
		names = append(names, namespace.Name)
	}
	return names, nil
}
