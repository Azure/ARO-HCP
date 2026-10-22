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

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	// TODO: read these from config.yaml
	exporterNamespace   = "aro-hcp-exporter"
	exporterServiceName = "aro-hcp-exporter"
	exporterServicePort = 8080

	// exporterMetricsTimeout bounds how long we wait for both collectors to
	// publish. A healthy exporter gets there within a couple of collection
	// intervals (collectionInterval is 2m and the Kusto collector self-throttles
	// to 5m), so a longer wait does not buy correctness — it just holds a
	// parallel worker hostage while an already-doomed run plays out.
	exporterMetricsTimeout = 10 * time.Minute
	exporterMetricsPolling = 30 * time.Second
)

// expectedExporterMetrics must all be present in a single scrape.
var expectedExporterMetrics = []string{
	"public_ip_count_by_region_service_tag",
	"kusto_logs_age_in_seconds",
}

// exporterHealthMetricPrefixes select the samples the exporter emits
// unconditionally. They explain *why* expectedExporterMetrics are missing:
// collectors serve cached const-metrics, so a collector that produced nothing
// contributes no output at all and is otherwise indistinguishable from one that
// is wedged. In particular a
// exporter_cluster_discovery_last_success_timestamp_seconds of 0 means cluster
// discovery never succeeded and every collector is blocked behind it.
var exporterHealthMetricPrefixes = []string{
	"exporter_cluster_discovery_",
	"exporter_collector_errors_total",
	"exporter_collector_duration_seconds_count",
}

var _ = Describe("Engineering", func() {
	It("should be able to retrieve expected metrics from the /metrics endpoint",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			By("building a Kubernetes client from KUBECONFIG")
			restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				clientcmd.NewDefaultClientConfigLoadingRules(),
				&clientcmd.ConfigOverrides{},
			).ClientConfig()
			Expect(err).NotTo(HaveOccurred(), "Failed to load kubeconfig")

			kubeClient, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")

			var localPort int
			var stopChan chan struct{}
			connected := false
			disconnect := func() {
				if connected {
					close(stopChan)
					connected = false
				}
			}
			defer disconnect()

			httpClient := http.Client{
				Timeout: 30 * time.Second,
			}

			By("polling the /metrics endpoint until every expected metric is published")
			Eventually(func() error {
				// (Re-)establish port-forward if needed.
				if !connected {
					podName, err := getExporterPodName(cancelCtx, kubeClient)
					if err != nil {
						return fmt.Errorf("no running exporter pod: %w", err)
					}
					localPort, stopChan, err = startPortForward(cancelCtx, restConfig, podName)
					if err != nil {
						return fmt.Errorf("failed to port-forward to exporter pod %q: %w", podName, err)
					}
					connected = true
				}

				metricsURL := fmt.Sprintf("http://localhost:%d/metrics", localPort)
				req, err := http.NewRequestWithContext(cancelCtx, http.MethodGet, metricsURL, nil)
				if err != nil {
					return fmt.Errorf("failed to build request for %s: %w", metricsURL, err)
				}

				resp, err := httpClient.Do(req)
				if err != nil {
					disconnect()
					return fmt.Errorf("failed to GET %s: %w", metricsURL, err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("GET %s returned %s, want 200 OK", metricsURL, resp.Status)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					disconnect()
					return fmt.Errorf("failed to read response body from %s: %w", metricsURL, err)
				}
				metricsOutput := string(body)

				var missing []string
				for _, name := range expectedExporterMetrics {
					if !strings.Contains(metricsOutput, name) {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					return fmt.Errorf("exporter has not published %s; exporter health metrics:\n%s",
						strings.Join(missing, ", "),
						extractMetricLines(metricsOutput, exporterHealthMetricPrefixes),
					)
				}
				return nil
			}).WithTimeout(exporterMetricsTimeout).WithPolling(exporterMetricsPolling).Should(Succeed(),
				"exporter never published all of %s within %s", strings.Join(expectedExporterMetrics, ", "), exporterMetricsTimeout)
		})
})

func getExporterPodName(ctx context.Context, kubeClient kubernetes.Interface) (string, error) {
	svc, err := kubeClient.CoreV1().Services(exporterNamespace).Get(ctx, exporterServiceName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service %s/%s: %w", exporterNamespace, exporterServiceName, err)
	}

	selector := selectorToString(svc.Spec.Selector)
	pods, err := kubeClient.CoreV1().Pods(exporterNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods with selector %q: %w", selector, err)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("no running pod found for service %s/%s (selector: %s)", exporterNamespace, exporterServiceName, selector)
}

// extractMetricLines returns the sample lines whose metric name starts with one
// of the given prefixes, so a failure message carries the exporter's own health
// signals instead of the entire scrape.
func extractMetricLines(metricsOutput string, prefixes []string) string {
	var lines []string
	for _, line := range strings.Split(metricsOutput, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				lines = append(lines, "  "+line)
				break
			}
		}
	}
	if len(lines) == 0 {
		return "  (none found in scrape)"
	}
	return strings.Join(lines, "\n")
}

func selectorToString(selector map[string]string) string {
	parts := make([]string, 0, len(selector))
	for k, v := range selector {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ",")
}

func startPortForward(ctx context.Context, restConfig *rest.Config, podName string) (int, chan struct{}, error) {
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create round tripper: %w", err)
	}

	pfURL := &url.URL{
		Scheme: "https",
		Host:   strings.TrimPrefix(strings.TrimPrefix(restConfig.Host, "https://"), "http://"),
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", exporterNamespace, podName),
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, pfURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", exporterServicePort)}, stopChan, readyChan, GinkgoWriter, GinkgoWriter)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create port forwarder: %w", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- fw.ForwardPorts()
	}()

	select {
	case err := <-errChan:
		return 0, nil, fmt.Errorf("port-forward failed: %w", err)
	case <-readyChan:
	case <-ctx.Done():
		close(stopChan)
		return 0, nil, ctx.Err()
	}

	ports, err := fw.GetPorts()
	if err != nil {
		close(stopChan)
		return 0, nil, fmt.Errorf("failed to get forwarded ports: %w", err)
	}

	return int(ports[0].Local), stopChan, nil
}
