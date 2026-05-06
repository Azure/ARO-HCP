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

package verifiers

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/onsi/ginkgo/v2"
	"go.yaml.in/yaml/v2"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

//go:embed artifacts
var staticFiles embed.FS

type webAppConfig struct {
	nodeSelector map[string]string
	routeLabels  map[string]string
	routeHost    string
}

type WebAppOption func(*webAppConfig)

func WithNodeSelector(nodeSelector map[string]string) WebAppOption {
	return func(c *webAppConfig) {
		c.nodeSelector = nodeSelector
	}
}

func WithRouteLabels(labels map[string]string) WebAppOption {
	return func(c *webAppConfig) {
		c.routeLabels = labels
	}
}

func WithRouteHost(host string) WebAppOption {
	return func(c *webAppConfig) {
		c.routeHost = host
	}
}

type verifySimpleWebApp struct {
	namespaceName string
	config        webAppConfig
}

func (v verifySimpleWebApp) Name() string {
	return "VerifySimpleWebApp"
}

func (v verifySimpleWebApp) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	klog.SetOutput(ginkgo.GinkgoWriter)
	defer func() {
		if err := v.cleanup(ctx, adminRESTConfig); err != nil {
			klog.ErrorS(err, "Error cleaning up resources")
		}
	}()

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace, err := kubeClient.CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "e2e-serving-app-",
			},
		},
		metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	deploymentYAML := must(staticFiles.ReadFile("artifacts/serving_app/deployment.yaml"))

	if v.config.nodeSelector != nil {

		var deploymentMap map[string]any
		if err := yaml.Unmarshal(deploymentYAML, &deploymentMap); err != nil {
			return fmt.Errorf("failed to unmarshal deployment YAML: %w", err)
		}

		if spec, ok := deploymentMap["spec"].(map[string]any); ok {
			if template, ok := spec["template"].(map[string]any); ok {
				if templateSpec, ok := template["spec"].(map[string]any); ok {
					templateSpec["nodeSelector"] = v.config.nodeSelector
				}
			}
		}

		deploymentYAML, err = yaml.Marshal(deploymentMap)
		if err != nil {
			return fmt.Errorf("failed to marshal modified deployment: %w", err)
		}
	}

	deployment, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, deploymentYAML)
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	service, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, must(staticFiles.ReadFile("artifacts/serving_app/service.yaml")))
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	routeYAML, err := buildRouteYAML(v.config.routeLabels, v.config.routeHost)
	if err != nil {
		return fmt.Errorf("failed to build route YAML: %w", err)
	}
	route, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, routeYAML)
	if err != nil {
		return fmt.Errorf("failed to create route: %w", err)
	}
	klog.InfoS("created resources",
		"namespace", namespace.GetName(),
		"deployment", deployment.GetName(),
		"service", service.GetName(),
		"route", route.GetName(),
	)

	// check for route to have hostname for us
	host := ""
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 25*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		currRoute, err := dynamicClient.Resource(gvr("route.openshift.io", "v1", "routes")).
			Namespace(namespace.GetName()).Get(ctx, route.GetName(), metav1.GetOptions{})
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				klog.Info(err, "failed to get route",
					"namespace", namespace.GetName(),
					"route", route.GetName(),
				)
			}
			lastErr = err
			return false, nil
		}
		host, _, _ = unstructured.NestedString(currRoute.Object, "spec", "host")
		if len(host) > 0 {
			return true, nil
		}

		return true, err
	})
	switch {
	case err == nil:
		// continue
	case lastErr != nil:
		klog.ErrorS(lastErr, "failed to get route",
			"namespace", namespace.GetName(),
			"route", route.GetName(),
		)
		return fmt.Errorf("route host was never found: %w", lastErr)
	case err != nil:
		return fmt.Errorf("route host was never found: %w", err)
	}

	// wait for a response
	lastErr = nil
	url := "https://" + host

	// Create HTTP client with TLS skip for development environments
	client := &http.Client{}
	if framework.IsDevelopmentEnvironment() {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	startTime := time.Now()
	logged5Min := false
	logged10Min := false
	logged15Min := false
	// firstResponseReceived := false
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 25*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		elapsed := time.Since(startTime)

		// Log progress messages at specific intervals
		if elapsed >= 15*time.Minute && !logged15Min {
			ginkgo.GinkgoWriter.Printf("Route availability check is taking over 15 minutes: url=%s elapsed=%v\n", url, elapsed)
			logged15Min = true
		} else if elapsed >= 10*time.Minute && !logged10Min {
			ginkgo.GinkgoWriter.Printf("Route availability check is taking between 10-15 minutes: url=%s elapsed=%v\n", url, elapsed)
			logged10Min = true
		} else if elapsed >= 5*time.Minute && !logged5Min {
			ginkgo.GinkgoWriter.Printf("Route availability check is taking between 5-10 minutes: url=%s elapsed=%v\n", url, elapsed)
			logged5Min = true
		}
		resp, err := client.Get(url)
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				klog.Info(err, "failed to get response from route",
					"url", url,
				)
			}
			lastErr = err
			return false, nil
		}
		defer resp.Body.Close()

		// Check for successful HTTP status code (200)
		if resp.StatusCode != 200 {
			statusErr := fmt.Errorf("received non-success status code: %d %s", resp.StatusCode, resp.Status)
			if lastErr == nil || statusErr.Error() != lastErr.Error() {
				ginkgo.GinkgoWriter.Printf("%s: route returned non-success status code", statusErr)
			}
			lastErr = statusErr
			return false, nil
		}

		responseByte, err := httputil.DumpResponse(resp, true)
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				klog.Info(err, "failed to read response from route",
					"url", url,
				)
			}
			lastErr = err
			return false, nil
		}

		// Log timing information for successful response
		elapsed = time.Since(startTime)
		if elapsed < 5*time.Minute {
			ginkgo.GinkgoWriter.Printf("Route became available in less than 5 minutes: url=%s elapsed=%v\n", url, elapsed)
		}
		ginkgo.GinkgoWriter.Printf("got successful response from route: response=%s\n", string(responseByte))
		return true, nil
	})
	switch {
	case err == nil:
		// continue
	case lastErr != nil:
		klog.ErrorS(lastErr, "failed to get or read response from route",
			"url", url,
		)
		return fmt.Errorf("route host was never found: %w", lastErr)
	case err != nil:
		return fmt.Errorf("route host was never found: %w", err)
	}

	return nil
}

func gvr(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}
}

func (v verifySimpleWebApp) cleanup(ctx context.Context, adminRESTConfig *rest.Config) error {
	if len(v.namespaceName) == 0 {
		return nil
	}
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	err = kubeClient.CoreV1().Namespaces().Delete(ctx, v.namespaceName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete namespace %q: %w", v.namespaceName, err)
	}

	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, false,
		func(ctx context.Context) (bool, error) {
			_, err := kubeClient.CoreV1().Namespaces().Get(ctx, v.namespaceName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			if err != nil {
				klog.ErrorS(err, "failed to get namespace", "namespace", v.namespaceName)
			}
			return false, nil
		})
	if err != nil {
		return fmt.Errorf("failed to cleanup namespace %q: %w", v.namespaceName, err)
	}

	return nil
}

func VerifySimpleWebApp(opts ...WebAppOption) HostedClusterVerifier {
	var cfg webAppConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return verifySimpleWebApp{config: cfg}
}

func buildRouteYAML(routeLabels map[string]string, routeHost string) ([]byte, error) {
	routeYAML := must(staticFiles.ReadFile("artifacts/serving_app/route.yaml"))
	if routeLabels == nil && len(routeHost) == 0 {
		return routeYAML, nil
	}

	var routeMap map[string]any
	if err := yaml.Unmarshal(routeYAML, &routeMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal route YAML: %w", err)
	}

	if routeLabels != nil {
		if metadata, ok := routeMap["metadata"].(map[string]any); ok {
			existing, _ := metadata["labels"].(map[string]any)
			if existing == nil {
				existing = make(map[string]any)
			}
			for k, val := range routeLabels {
				existing[k] = val
			}
			metadata["labels"] = existing
		}
	}

	if len(routeHost) > 0 {
		if spec, ok := routeMap["spec"].(map[string]any); ok {
			spec["host"] = routeHost
		}
	}

	return yaml.Marshal(routeMap)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic("error: " + err.Error())
	}
	return v
}
