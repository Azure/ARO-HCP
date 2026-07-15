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
	"net"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
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

	if v.nodeSelector != nil {

		var deploymentMap map[string]any
		if err := yaml.Unmarshal(deploymentYAML, &deploymentMap); err != nil {
			return fmt.Errorf("failed to unmarshal deployment YAML: %w", err)
		}

		if spec, ok := deploymentMap["spec"].(map[string]any); ok {
			if template, ok := spec["template"].(map[string]any); ok {
				if templateSpec, ok := template["spec"].(map[string]any); ok {
					templateSpec["nodeSelector"] = v.nodeSelector
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

	url := "https://" + host

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
	if err := waitForTrustedRouteReachability(ctx, &http.Client{Transport: secureTransport}, url, host, 10*time.Minute); err != nil {
		printNegotiatedCertificate(ctx, host)
		collectIngressCertDiagnostics(ctx, kubeClient, dynamicClient)
		return err
	}

	return nil
}

// servedCertificate describes the leaf certificate currently served by the
// router, and whether it is still the operator-generated self-signed default
// ingress certificate rather than the managed OneCert-issued certificate.
type servedCertificate struct {
	desc       string
	selfSigned bool
	served     bool
}

// classifyServedCertificate dials host:443 (skipping verification) and reports
// the leaf certificate currently served. The managed ingress certificate is
// delivered asynchronously (OneCert -> Key Vault -> ACM -> IngressController),
// so until it lands the router serves a self-signed default certificate
// (subject CN=openshift-ingress, issuer CN=root-ca). Distinguishing the two
// lets the strict TLS wait log the self-signed -> managed transition.
func classifyServedCertificate(ctx context.Context, host string) servedCertificate {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(dialCtx, "tcp", host+":443")
	if err != nil {
		return servedCertificate{desc: fmt.Sprintf("dial failed: %v", err)}
	}
	defer conn.Close()

	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return servedCertificate{desc: "no certificate served"}
	}
	leaf := certs[0]
	selfSigned := strings.Contains(leaf.Issuer.CommonName, "root-ca") ||
		strings.EqualFold(leaf.Subject.CommonName, "openshift-ingress")
	return servedCertificate{
		desc: fmt.Sprintf("subject=%q issuer=%q notBefore=%s notAfter=%s",
			leaf.Subject, leaf.Issuer,
			leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339)),
		selfSigned: selfSigned,
		served:     true,
	}
}

// certKindLabel returns a human-readable label for a served certificate.
func certKindLabel(c servedCertificate) string {
	if c.selfSigned {
		return "self-signed default"
	}
	return "managed (OneCert)"
}

// waitForTrustedRouteReachability polls url requiring strict TLS trust, logging
// each observed certificate state so the progression self-signed default ->
// waiting -> managed (OneCert) trusted certificate is visible in the test
// output. Managed ingress-cert delivery occasionally stalls (the guest
// cluster-ingress-cert secret is never created), and this makes such a stall
// diagnosable inline rather than surfacing only as a generic x509 "unknown
// authority" error.
func waitForTrustedRouteReachability(ctx context.Context, client *http.Client, url, host string, timeout time.Duration) error {
	var lastErr error
	startTime := time.Now()
	lastDesc := ""
	lastStatusLog := time.Time{}

	// Record the certificate served before strict verification begins so the
	// starting point (typically the self-signed default) is captured.
	if initial := classifyServedCertificate(ctx, host); initial.served {
		lastDesc = initial.desc
		ginkgo.GinkgoWriter.Printf("[strict-tls] initial certificate served by %s is the %s certificate: %s\n",
			host, certKindLabel(initial), initial.desc)
	}

	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		elapsed := time.Since(startTime).Round(time.Second)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, fmt.Errorf("failed to build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			// Surface which certificate is currently served whenever it changes,
			// or at least once a minute, while waiting for the managed cert.
			c := classifyServedCertificate(ctx, host)
			if c.served && (c.desc != lastDesc || time.Since(lastStatusLog) >= time.Minute) {
				ginkgo.GinkgoWriter.Printf("[strict-tls] waiting for trusted managed certificate (elapsed=%v): route not yet trusted (%v); currently serving %s certificate: %s\n",
					elapsed, err, certKindLabel(c), c.desc)
				lastDesc = c.desc
				lastStatusLog = time.Now()
			}
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("received non-success status code: %d %s", resp.StatusCode, resp.Status)
			return false, nil
		}

		c := classifyServedCertificate(ctx, host)
		if c.served {
			ginkgo.GinkgoWriter.Printf("[strict-tls] route reachable with trusted %s certificate after %v: %s\n",
				certKindLabel(c), elapsed, c.desc)
		} else {
			ginkgo.GinkgoWriter.Printf("[strict-tls] route reachable with a trusted certificate after %v, but re-classifying the served certificate failed: %s\n",
				elapsed, c.desc)
		}
		return true, nil
	})

	switch {
	case err == nil:
		return nil
	case lastErr != nil:
		return fmt.Errorf("route was never reachable with a trusted certificate: %w", lastErr)
	default:
		return fmt.Errorf("route was never reachable with a trusted certificate: %w", err)
	}
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
				klog.Info(err, "failed to get response from route",
					"url", url,
				)
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

const (
	ingressNamespace         = "openshift-ingress"
	ingressOperatorNamespace = "openshift-ingress-operator"
	managedIngressCertSecret = "cluster-ingress-cert"
	defaultIngressCertSecret = "default-ingress-cert"
)

// collectIngressCertDiagnostics inspects the guest cluster's ingress serving
// certificate delivery chain and logs its state. It runs when the strict TLS
// wait fails so CI captures why the managed certificate never became trusted.
//
// The managed serving certificate (openshift-ingress/cluster-ingress-cert) is
// delivered from the ACM hub by a ConfigurationPolicy; its absence leaves the
// router serving the ingress operator's self-signed default certificate, which
// fails strict verification with "x509: certificate signed by unknown
// authority". Dumping the managed-vs-default secret presence, the
// IngressController spec/status, the router pod states, and the ingress
// namespace warning events distinguishes a stalled secret delivery (the
// observed failure mode) from an IngressController that never reconciled or a
// router that never rolled out.
func collectIngressCertDiagnostics(ctx context.Context, kubeClient kubernetes.Interface, dynamicClient dynamic.Interface) {
	out := ginkgo.GinkgoWriter
	out.Printf("[strict-tls][diag] collecting guest ingress certificate delivery diagnostics\n")

	// Bound the best-effort diagnostics so a slow/unresponsive apiserver cannot
	// delay returning the original strict-TLS failure. All API calls below reuse
	// this short-lived context.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// 1) Serving-cert secrets in the ingress namespace. The managed secret is
	// expected once delivery succeeds; the self-signed default is what the
	// router falls back to while the managed secret is missing.
	for _, name := range []string{managedIngressCertSecret, defaultIngressCertSecret} {
		secret, err := kubeClient.CoreV1().Secrets(ingressNamespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			out.Printf("[strict-tls][diag] secret %s/%s: NOT FOUND\n", ingressNamespace, name)
		case err != nil:
			out.Printf("[strict-tls][diag] secret %s/%s: error getting: %v\n", ingressNamespace, name, err)
		default:
			keys := make([]string, 0, len(secret.Data))
			for k := range secret.Data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.Printf("[strict-tls][diag] secret %s/%s: PRESENT type=%s dataKeys=%v created=%s\n",
				ingressNamespace, name, secret.Type, keys, secret.CreationTimestamp.Format(time.RFC3339))
		}
	}

	// 2) IngressController/default: the configured default certificate and any
	// not-ready status conditions.
	ic, err := dynamicClient.Resource(gvr("operator.openshift.io", "v1", "ingresscontrollers")).
		Namespace(ingressOperatorNamespace).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		out.Printf("[strict-tls][diag] ingresscontroller %s/default: error getting: %v\n", ingressOperatorNamespace, err)
	} else {
		defaultCert, _, _ := unstructured.NestedString(ic.Object, "spec", "defaultCertificate", "name")
		if defaultCert == "" {
			out.Printf("[strict-tls][diag] ingresscontroller default: spec.defaultCertificate.name is UNSET (operator self-signed default in use)\n")
		} else {
			out.Printf("[strict-tls][diag] ingresscontroller default: spec.defaultCertificate.name=%q\n", defaultCert)
		}
		conditions, _, _ := unstructured.NestedSlice(ic.Object, "status", "conditions")
		for _, c := range conditions {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			ctype, _, _ := unstructured.NestedString(cm, "type")
			cstatus, _, _ := unstructured.NestedString(cm, "status")
			// Only surface conditions that indicate a not-ready/degraded state.
			switch ctype {
			case "Available":
				if cstatus == "True" {
					continue
				}
			case "Degraded", "Progressing":
				if cstatus == "False" {
					continue
				}
			default:
				continue
			}
			creason, _, _ := unstructured.NestedString(cm, "reason")
			cmsg, _, _ := unstructured.NestedString(cm, "message")
			out.Printf("[strict-tls][diag] ingresscontroller default condition %s=%s reason=%q message=%q\n",
				ctype, cstatus, creason, cmsg)
		}
	}

	// 3) Router pods: a missing managed secret keeps the rolled-out router
	// replicaset in ContainerCreating with FailedMount, so report any container
	// stuck waiting.
	pods, err := kubeClient.CoreV1().Pods(ingressNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "ingresscontroller.operator.openshift.io/deployment-ingresscontroller=default",
	})
	if err != nil {
		out.Printf("[strict-tls][diag] router pods: error listing: %v\n", err)
	} else {
		out.Printf("[strict-tls][diag] router pods: %d found\n", len(pods.Items))
		for i := range pods.Items {
			pod := &pods.Items[i]
			out.Printf("[strict-tls][diag]   pod %s phase=%s created=%s\n",
				pod.Name, pod.Status.Phase, pod.CreationTimestamp.Format(time.RFC3339))
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					out.Printf("[strict-tls][diag]     container %s waiting: reason=%s message=%q\n",
						cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
			}
		}
	}

	// 4) Warning events in the ingress namespace (e.g. FailedMount of the
	// managed secret).
	events, err := kubeClient.CoreV1().Events(ingressNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=Warning",
		Limit:         50,
	})
	if err != nil {
		out.Printf("[strict-tls][diag] events: error listing: %v\n", err)
	} else {
		const maxWarnings = 20
		printed := 0
		for i := range events.Items {
			e := &events.Items[i]
			if e.Type != corev1.EventTypeWarning {
				continue
			}
			ts, age := eventTimestamp(e)
			out.Printf("[strict-tls][diag]   event %s/%s reason=%s count=%d lastSeen=%s age=%s message=%q\n",
				e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Reason, e.Count,
				ts, age, e.Message)
			printed++
			if printed >= maxWarnings {
				out.Printf("[strict-tls][diag]   (truncated additional warning events)\n")
				break
			}
		}
		if printed == 0 {
			out.Printf("[strict-tls][diag] events: no warning events in %s\n", ingressNamespace)
		}
	}
}

// eventTimestamp returns a formatted best-available timestamp for an event and
// its age. LastTimestamp is frequently zero on modern clusters (events API), so
// it falls back to EventTime then CreationTimestamp to keep the output
// actionable instead of printing 0001-01-01.
func eventTimestamp(e *corev1.Event) (string, string) {
	t := e.LastTimestamp.Time
	if t.IsZero() {
		t = e.EventTime.Time
	}
	if t.IsZero() {
		t = e.CreationTimestamp.Time
	}
	if t.IsZero() {
		return "unknown", "unknown"
	}
	return t.Format(time.RFC3339), time.Since(t).Round(time.Second).String()
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

func VerifySimpleWebApp(nodeSelector ...map[string]string) HostedClusterVerifier {
	var ns map[string]string
	if len(nodeSelector) > 0 {
		ns = nodeSelector[0]
	}
	return verifySimpleWebApp{nodeSelector: ns}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic("error: " + err.Error())
	}
	return v
}
