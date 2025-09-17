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
	"embed"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/onsi/ginkgo/v2"
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
)

//go:embed artifacts
var staticFiles embed.FS

type verifySimpleWebApp struct {
	namespaceName string
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

	deployment, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, must(staticFiles.ReadFile("artifacts/serving_app/deployment.yaml")))
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	service, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, must(staticFiles.ReadFile("artifacts/serving_app/service.yaml")))
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	route, err := createArbitraryResource(ctx, dynamicClient, namespace.Name, must(staticFiles.ReadFile("artifacts/serving_app/route.yaml")))
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
	startTime := time.Now()
	logged5Min := false
	logged10Min := false
	logged15Min := false
	firstResponseReceived := false
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
		resp, err := http.Get(url)
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

		// Check for successful HTTP status code (200-299)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			statusErr := fmt.Errorf("received non-success status code: %d %s", resp.StatusCode, resp.Status)
			if lastErr == nil || statusErr.Error() != lastErr.Error() {
				klog.Info(statusErr, "route returned non-success status code",
					"url", url,
					"statusCode", resp.StatusCode,
				)
			}
			lastErr = statusErr

			// If this is the first response we've received, start retrying for 3 minutes
			if !firstResponseReceived {
				firstResponseReceived = true
				firstResponseTime := time.Now()
				ginkgo.GinkgoWriter.Printf("Got first response with status %d, will retry for 3 minutes: url=%s\n", resp.StatusCode, url)

				// Retry every 10 seconds for 3 minutes
				retryErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					retryResp, retryErr := http.Get(url)
					if retryErr != nil {
						return false, nil
					}
					defer retryResp.Body.Close()

					if retryResp.StatusCode >= 200 && retryResp.StatusCode < 300 {
						successTime := time.Since(firstResponseTime)
						ginkgo.GinkgoWriter.Printf("Route became healthy after %v from first response: url=%s statusCode=%d\n", successTime, url, retryResp.StatusCode)

						// Dump the successful response
						responseByte, _ := httputil.DumpResponse(retryResp, true)
						ginkgo.GinkgoWriter.Printf("got successful response from route: response=%s\n", string(responseByte))
						return true, nil
					}
					return false, nil
				})

				if retryErr != nil {
					return false, nil
				}
				return true, nil
			}
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
		if elapsed := time.Since(startTime); elapsed < 5*time.Minute {
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

func VerifySimpleWebApp() HostedClusterVerifier {
	return verifySimpleWebApp{}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic("error: " + err.Error())
	}
	return v
}
