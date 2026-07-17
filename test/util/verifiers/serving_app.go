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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/onsi/ginkgo/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

//go:embed artifacts
var staticFiles embed.FS

type verifySimpleWebApp struct {
	namespaceName string
	nodeSelector  map[string]string
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

	app, err := framework.DeploySampleApp(ctx, adminRESTConfig, v.nodeSelector)
	if err != nil {
		return err
	}
	v.namespaceName = app.Namespace

	url := "https://" + app.RouteHost

	if err := framework.WaitForDNSResolution(ctx, app.RouteHost, framework.DNSResolutionTimeout); err != nil {
		return fmt.Errorf("DNS for route host %s did not resolve: %w", app.RouteHost, err)
	}

	// First wait for app reachability using InsecureSkipVerify.
	// Cert provisioning (OneCert -> Key Vault -> ACM -> IngressController) has
	// variable latency, so this proves app availability independently of trust.
	insecureTransport := http.DefaultTransport.(*http.Transport).Clone()
	insecureTransport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}
	if err := waitForRouteReachability(ctx, &http.Client{Transport: insecureTransport}, url, 25*time.Minute); err != nil {
		return err
	}

	// Then require strict TLS verification on the same route reachability check.
	if framework.IsDevelopmentEnvironment() {
		ginkgo.GinkgoWriter.Printf("Skipping strict TLS route reachability in development environment\n")
		return nil
	}
	secureTransport := http.DefaultTransport.(*http.Transport).Clone()
	if err := waitForRouteReachability(ctx, &http.Client{Transport: secureTransport}, url, 10*time.Minute); err != nil {
		printNegotiatedCertificate(ctx, app.RouteHost)
		return err
	}

	return nil
}

// waitForRouteReachability polls the URL with the provided client until a
// successful HTTP 200 response is observed or timeout is reached.
func waitForRouteReachability(ctx context.Context, client *http.Client, url string, timeout time.Duration) error {
	var lastErr error
	startTime := time.Now()
	logged5Min := false
	logged10Min := false
	logged15Min := false

	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		elapsed := time.Since(startTime)

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
				var dnsErr *net.DNSError
				if errors.As(err, &dnsErr) {
					klog.Infof("DNS error for route %s: server=%s isNotFound=%v isTemporary=%v err=%s",
						url, dnsErr.Server, dnsErr.IsNotFound, dnsErr.IsTemporary, dnsErr.Err)
				} else {
					klog.InfoS("failed to get response from route",
						"url", url,
						"error", err,
					)
				}
			}
			lastErr = err
			return false, nil
		}
		defer resp.Body.Close()

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

		elapsed = time.Since(startTime)
		if elapsed < 5*time.Minute {
			ginkgo.GinkgoWriter.Printf("Route became available in less than 5 minutes: url=%s elapsed=%v\n", url, elapsed)
		}
		ginkgo.GinkgoWriter.Printf("got successful response from route: response=%s\n", string(responseByte))
		return true, nil
	})

	switch {
	case err == nil:
		return nil
	case lastErr != nil:
		klog.ErrorS(lastErr, "failed to get or read response from route",
			"url", url,
		)
		return fmt.Errorf("route was never reachable: %w", lastErr)
	default:
		return fmt.Errorf("route was never reachable: %w", err)
	}
}

// printNegotiatedCertificate logs the leaf certificate currently negotiated for
// host to aid strict TLS reachability diagnostics.
func printNegotiatedCertificate(ctx context.Context, host string) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(dialCtx, "tcp", host+":443")
	if err != nil {
		ginkgo.GinkgoWriter.Printf("failed to dial host to print negotiated certificate: host=%s err=%v\n", host, err)
		return
	}
	defer conn.Close()

	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		ginkgo.GinkgoWriter.Printf("no certificates served by %s\n", host)
		return
	}
	ginkgo.GinkgoWriter.Printf("Negotiated certificate: host=%s subject=%v issuer=%v dnsNames=%v notBefore=%v notAfter=%v\n",
		host,
		certs[0].Subject,
		certs[0].Issuer,
		certs[0].DNSNames,
		certs[0].NotBefore,
		certs[0].NotAfter,
	)
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

func VerifySimpleWebApp(nodeSelector ...map[string]string) HostedClusterVerifier {
	var ns map[string]string
	if len(nodeSelector) > 0 {
		ns = nodeSelector[0]
	}
	return verifySimpleWebApp{nodeSelector: ns}
}
