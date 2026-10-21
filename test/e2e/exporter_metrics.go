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
)

var _ = Describe("Engineering", func() {
	It("should be able to retrieve expected metrics from the /metrics endpoint",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
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
			defer func() {
				if connected {
					close(stopChan)
				}
			}()

			httpClient := http.Client{
				Timeout: 30 * time.Second,
			}
			Eventually(func() bool {
				// (Re-)establish port-forward if needed.
				if !connected {
					By("finding a pod for the aro-hcp-exporter service")
					podName, err := getExporterPodName(cancelCtx, kubeClient)
					if err != nil {
						return false
					}
					By("port-forwarding to the exporter pod")
					localPort, stopChan, err = startPortForward(cancelCtx, restConfig, podName)
					if err != nil {
						return false
					}
					connected = true
				}

				By("querying the /metrics endpoint")
				metricsURL := fmt.Sprintf("http://localhost:%d/metrics", localPort)
				req, err := http.NewRequestWithContext(cancelCtx, http.MethodGet, metricsURL, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to create HTTP request for metrics endpoint")

				resp, err := httpClient.Do(req)
				if err != nil {
					GinkgoWriter.Printf("Failed to get metrics endpoint: %v", err)
					close(stopChan)
					connected = false
					return false
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return false
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					GinkgoWriter.Printf("Failed to read metrics response body: %v", err)
					close(stopChan)
					connected = false
					return false
				}

				metricsOutput := string(body)

				By("verifying public_ip_count_by_region_service_tag is present")
				if !strings.Contains(metricsOutput, "public_ip_count_by_region_service_tag") {
					return false
				}

				By("verifying kusto_logs_age_in_seconds is present")
				return strings.Contains(metricsOutput, "kusto_logs_age_in_seconds")
			}).WithTimeout(30 * time.Minute).WithPolling(30 * time.Second).Should(BeTrue())
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
