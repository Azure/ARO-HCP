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
	"crypto/x509"
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
	if err := waitForRouteReachability(ctx, &http.Client{Transport: insecureTransport}, url, 25*time.Minute, "insecure reachability"); err != nil {
		return err
	}

	// Then require strict TLS verification on the same route reachability check.
	if framework.IsDevelopmentEnvironment() {
		ginkgo.GinkgoWriter.Printf("Skipping strict TLS route reachability in development environment\n")
		return nil
	}
	secureTransport := http.DefaultTransport.(*http.Transport).Clone()
	if err := waitForRouteReachability(ctx, &http.Client{Transport: secureTransport}, url, 10*time.Minute, "strict TLS verification"); err != nil {
		printTLSErrorDetails("strict TLS verification", err)
		printNegotiatedCertificate(ctx, "strict TLS verification", app.RouteHost)
		return err
	}

	return nil
}

var routeReachabilityPollInterval = 10 * time.Second

// waitForRouteReachability polls the URL with the provided client until a
// successful HTTP 200 response is observed or timeout is reached. The phase
// string is included in all log messages to distinguish check stages.
func waitForRouteReachability(ctx context.Context, client *http.Client, url string, timeout time.Duration, phase string) error {
	var lastErr error
	var dnsFailureCount int
	var firstDNSFailureTime time.Time
	startTime := time.Now()
	logged5Min := false
	logged10Min := false
	logged15Min := false

	err := wait.PollUntilContextTimeout(ctx, routeReachabilityPollInterval, timeout, true, func(ctx context.Context) (done bool, err error) {
		elapsed := time.Since(startTime)

		if elapsed >= 15*time.Minute && !logged15Min {
			ginkgo.GinkgoWriter.Printf("[%s] Route check is taking over 15 minutes: url=%s elapsed=%v\n", phase, url, elapsed)
			logged15Min = true
		} else if elapsed >= 10*time.Minute && !logged10Min {
			ginkgo.GinkgoWriter.Printf("[%s] Route check is taking between 10-15 minutes: url=%s elapsed=%v\n", phase, url, elapsed)
			logged10Min = true
		} else if elapsed >= 5*time.Minute && !logged5Min {
			ginkgo.GinkgoWriter.Printf("[%s] Route check is taking between 5-10 minutes: url=%s elapsed=%v\n", phase, url, elapsed)
			logged5Min = true
		}
		resp, err := client.Get(url)
		if err != nil {
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) {
				dnsFailureCount++
				if firstDNSFailureTime.IsZero() {
					firstDNSFailureTime = time.Now()
				}
				dnsDuration := time.Since(firstDNSFailureTime)

				if dnsDuration > 5*time.Minute && dnsFailureCount%30 == 0 {
					ginkgo.GinkgoWriter.Printf("[%s] WARNING: DNS resolution failing for over 5 minutes (TTL period): url=%s host=%s duration=%v failures=%d\n",
						phase, url, dnsErr.Name, dnsDuration, dnsFailureCount)
				}

				if lastErr == nil || err.Error() != lastErr.Error() {
					klog.InfoS("DNS resolution failed (may indicate DNS propagation delay)",
						"phase", phase,
						"url", url,
						"host", dnsErr.Name,
						"dnsError", dnsErr.Err,
						"isTimeout", dnsErr.IsTimeout,
						"isTemporary", dnsErr.IsTemporary,
						"isNotFound", dnsErr.IsNotFound,
						"consecutiveDNSFailures", dnsFailureCount,
						"dnsDuration", dnsDuration,
					)
				}
				lastErr = err
				return false, nil
			}

			if dnsFailureCount > 0 {
				klog.InfoS("DNS resolution succeeded, but connection failed with different error",
					"phase", phase,
					"previousDNSFailures", dnsFailureCount,
					"dnsDuration", time.Since(firstDNSFailureTime),
				)
				dnsFailureCount = 0
				firstDNSFailureTime = time.Time{}
			}

			if lastErr == nil || err.Error() != lastErr.Error() {
				klog.InfoS("failed to get response from route",
					"phase", phase,
					"url", url,
					"error", err,
				)
			}
			lastErr = err
			return false, nil
		}

		if dnsFailureCount > 0 {
			klog.InfoS("DNS resolution and connection succeeded after previous DNS failures",
				"phase", phase,
				"totalDNSFailures", dnsFailureCount,
				"dnsDuration", time.Since(firstDNSFailureTime),
			)
			dnsFailureCount = 0
			firstDNSFailureTime = time.Time{}
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			statusErr := fmt.Errorf("received non-success status code: %d %s", resp.StatusCode, resp.Status)
			if lastErr == nil || statusErr.Error() != lastErr.Error() {
				ginkgo.GinkgoWriter.Printf("[%s] route returned non-success status code: %s\n", phase, statusErr)
			}
			lastErr = statusErr
			return false, nil
		}

		responseByte, err := httputil.DumpResponse(resp, true)
		if err != nil {
			if lastErr == nil || err.Error() != lastErr.Error() {
				klog.InfoS("failed to read response from route",
					"phase", phase,
					"url", url,
					"error", err,
				)
			}
			lastErr = err
			return false, nil
		}

		elapsed = time.Since(startTime)
		if elapsed < 5*time.Minute {
			ginkgo.GinkgoWriter.Printf("[%s] Route became available in less than 5 minutes: url=%s elapsed=%v\n", phase, url, elapsed)
		}
		ginkgo.GinkgoWriter.Printf("[%s] got successful response from route: response=%s\n", phase, string(responseByte))
		return true, nil
	})

	switch {
	case err == nil:
		return nil
	case lastErr != nil:
		klog.ErrorS(lastErr, "route check failed",
			"phase", phase,
			"url", url,
		)
		return fmt.Errorf("[%s] route was never reachable: %w", phase, lastErr)
	default:
		return fmt.Errorf("[%s] route was never reachable: %w", phase, err)
	}
}

// printTLSErrorDetails unwraps TLS certificate verification errors and logs
// the specific x509 error type and details to aid debugging.
func printTLSErrorDetails(phase string, err error) {
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		if cert := unknownAuthErr.Cert; cert != nil {
			ginkgo.GinkgoWriter.Printf("[%s] x509.UnknownAuthorityError: untrusted cert subject=%v issuer=%v isCA=%v\n",
				phase, cert.Subject, cert.Issuer, cert.IsCA,
			)
		} else {
			ginkgo.GinkgoWriter.Printf("[%s] x509.UnknownAuthorityError: cert=nil\n", phase)
		}
		return
	}
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certInvalidErr) {
		if cert := certInvalidErr.Cert; cert != nil {
			ginkgo.GinkgoWriter.Printf("[%s] x509.CertificateInvalidError: reason=%d cert subject=%v issuer=%v\n",
				phase, certInvalidErr.Reason, cert.Subject, cert.Issuer,
			)
		} else {
			ginkgo.GinkgoWriter.Printf("[%s] x509.CertificateInvalidError: reason=%d cert=nil\n", phase, certInvalidErr.Reason)
		}
		return
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		if cert := hostnameErr.Certificate; cert != nil {
			ginkgo.GinkgoWriter.Printf("[%s] x509.HostnameError: host=%s cert subject=%v dnsNames=%v\n",
				phase, hostnameErr.Host, cert.Subject, cert.DNSNames,
			)
		} else {
			ginkgo.GinkgoWriter.Printf("[%s] x509.HostnameError: host=%s cert=nil\n", phase, hostnameErr.Host)
		}
		return
	}
	ginkgo.GinkgoWriter.Printf("[%s] TLS error (no x509 details extracted): %v\n", phase, err)
}

// printNegotiatedCertificate logs the full peer certificate chain currently
// negotiated for host to aid strict TLS reachability diagnostics.
func printNegotiatedCertificate(ctx context.Context, phase string, host string) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(dialCtx, "tcp", host+":443")
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[%s] failed to dial host to print negotiated certificate: host=%s err=%v\n", phase, host, err)
		return
	}
	defer conn.Close()

	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		ginkgo.GinkgoWriter.Printf("[%s] no certificates served by %s\n", phase, host)
		return
	}
	ginkgo.GinkgoWriter.Printf("[%s] Peer certificate chain for %s (%d certificate(s)):\n", phase, host, len(certs))
	for i, cert := range certs {
		ginkgo.GinkgoWriter.Printf("  [%d] subject=%v issuer=%v dnsNames=%v notBefore=%v notAfter=%v isCA=%v\n",
			i,
			cert.Subject,
			cert.Issuer,
			cert.DNSNames,
			cert.NotBefore,
			cert.NotAfter,
			cert.IsCA,
		)
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

func VerifySimpleWebApp(nodeSelector ...map[string]string) HostedClusterVerifier {
	var ns map[string]string
	if len(nodeSelector) > 0 {
		ns = nodeSelector[0]
	}
	return verifySimpleWebApp{nodeSelector: ns}
}
