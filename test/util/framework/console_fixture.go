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

package framework

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"golang.org/x/crypto/bcrypt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	kuberand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	routev1 "github.com/openshift/api/route/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// Dex v2.43.1, verified by tag and digest against GHCR. Invoke dex directly to
// avoid the image entrypoint's config templating on a read-only root filesystem.
const consoleDexImage = "ghcr.io/dexidp/dex@sha256:0881d3c9359b436d585b2061736ce271c100331e073be9178ef405ce5bf09557"

// ConsoleFixture contains credentials for one test. Never log the whole value.
// Username is the Dex login email, not the prefixed Kubernetes username.
type ConsoleFixture struct {
	ConsoleURL, IssuerURL, Username, Password, Namespace, ConfigMapName, Marker string
	ConsoleCA, IssuerCA                                                         []byte
}

// SetupConsoleFixture20240610 installs an ephemeral issuer and configures Console
// through the RP. It requires a cluster in a TestContext-owned resource group:
// all resources (including partial setup) survive diagnostics and are destroyed
// by the already-registered cluster teardown, not an earlier DeferCleanup.
func (tc *perItOrDescribeTestContext) SetupConsoleFixture20240610(ctx context.Context, resourceGroup, clusterName string, adminConfig *rest.Config, timeout time.Duration) (fixture ConsoleFixture, setupErr error) {
	if timeout <= 0 || adminConfig == nil {
		return fixture, fmt.Errorf("console fixture requires a positive setup timeout and admin REST config")
	}
	tc.contextLock.RLock()
	owned := slices.Contains(tc.knownResourceGroups, resourceGroup)
	tc.contextLock.RUnlock()
	if !owned {
		return fixture, fmt.Errorf("console fixture requires a TestContext-owned resource group for cleanup")
	}
	factory := tc.Get20240610ClientFactoryOrDie(ctx)
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	config := rest.CopyConfig(adminConfig)
	config.Timeout = 30 * time.Second
	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fixture, consoleFixtureAPIError("construct admin client", err)
	}
	routes, err := routev1client.NewForConfig(config)
	if err != nil {
		return fixture, consoleFixtureAPIError("construct Route client", err)
	}
	err = waitConsoleFixture(ctx, "ARM-published Console URL", func(ctx context.Context) error {
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cluster, err := GetHCPCluster20240610(requestCtx, factory.NewHcpOpenShiftClustersClient(), resourceGroup, clusterName)
		if err != nil {
			return consoleFixtureAPIError("get ARM cluster", err)
		}
		if cluster.Properties == nil || cluster.Properties.Console == nil || cluster.Properties.Console.URL == nil {
			return fmt.Errorf("ARM cluster has not published properties.console.url")
		}
		fixture.ConsoleURL = *cluster.Properties.Console.URL
		return validateConsoleFixtureURL(fixture.ConsoleURL)
	})
	if err != nil {
		return fixture, err
	}
	if IsDevelopmentEnvironment() {
		// Same ingress trust source as library-go/test/library/metrics/query.go.
		err = waitConsoleFixture(ctx, "development ingress CA bundle", func(ctx context.Context) error {
			cm, err := kube.CoreV1().ConfigMaps("openshift-config-managed").Get(ctx, "default-ingress-cert", metav1.GetOptions{})
			if err != nil {
				return consoleFixtureAPIError("get default-ingress-cert", err)
			}
			fixture.ConsoleCA = []byte(cm.Data["ca-bundle.crt"])
			if !x509.NewCertPool().AppendCertsFromPEM(fixture.ConsoleCA) {
				return fmt.Errorf("default-ingress-cert ca-bundle.crt does not contain certificates")
			}
			return nil
		})
		if err != nil {
			return fixture, err
		}
	}
	// Outside development ConsoleCA stays nil: only system trust is permitted.
	// Reuse the namespace as the ExternalAuth name, whose API limit is 15 characters.
	fixture.Namespace = "console-" + kuberand.String(7)
	fixture.ConfigMapName = "login-marker"
	fixture.Marker = rand.Text()
	fixture.Username = fixture.Namespace + "@example.com"
	fixture.Password = rand.Text()
	clientSecret := rand.Text()
	if _, err := kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fixture.Namespace}}, metav1.CreateOptions{}); err != nil {
		fixture.Namespace = ""
		return fixture, consoleFixtureAPIError("create fixture namespace", err)
	}
	ginkgo.GinkgoLogr.Info("Created Console fixture namespace", "namespace", fixture.Namespace)
	defer func() {
		if setupErr != nil {
			setupErr = fmt.Errorf("console fixture namespace %q: %w; %s", fixture.Namespace, setupErr, ConsoleFixtureDiagnostics(parentCtx, adminConfig, fixture.Namespace))
		}
	}()
	route, err := routes.Routes(fixture.Namespace).Create(ctx, &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "dex", Namespace: fixture.Namespace},
		Spec: routev1.RouteSpec{
			To:   routev1.RouteTargetReference{Kind: "Service", Name: "dex"},
			Port: &routev1.RoutePort{TargetPort: intstr.FromString("https")},
			TLS:  &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough, InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fixture, consoleFixtureAPIError("create Dex passthrough Route", err)
	}
	if route.Spec.Host == "" {
		return fixture, fmt.Errorf("dex Route was created without an assigned host")
	}
	fixture.IssuerURL = "https://" + route.Spec.Host
	ca, cert, key, err := consoleFixtureCertificates(route.Spec.Host, time.Now())
	if err != nil {
		return fixture, fmt.Errorf("generate Dex TLS certificates: %w", err)
	}
	fixture.IssuerCA = ca
	objects, err := newConsoleFixtureObjects(fixture, clientSecret, cert, key)
	if err != nil {
		return fixture, err
	}
	if _, err := kube.CoreV1().Secrets(fixture.Namespace).Create(ctx, objects.dexSecret, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create Dex configuration Secret", err)
	}
	if _, err := kube.CoreV1().Services(fixture.Namespace).Create(ctx, objects.service, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create Dex Service", err)
	}
	if _, err := kube.AppsV1().Deployments(fixture.Namespace).Create(ctx, objects.deployment, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create Dex Deployment", err)
	}
	if _, err := kube.CoreV1().ConfigMaps(fixture.Namespace).Create(ctx, objects.marker, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create login marker ConfigMap", err)
	}
	if _, err := kube.RbacV1().Roles(fixture.Namespace).Create(ctx, objects.role, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create marker reader Role", err)
	}
	if _, err := kube.RbacV1().RoleBindings(fixture.Namespace).Create(ctx, objects.binding, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("bind Dex user to marker reader Role", err)
	}
	// The Console operator must find the secret on its first reconciliation.
	if _, err := kube.CoreV1().Secrets("openshift-config").Create(ctx, objects.consoleSecret, metav1.CreateOptions{}); err != nil {
		return fixture, consoleFixtureAPIError("create Console OIDC client Secret", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(fixture.IssuerCA)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	err = waitConsoleFixture(ctx, "Dex HTTPS issuer discovery", func(ctx context.Context) error {
		deployment, err := kube.AppsV1().Deployments(fixture.Namespace).Get(ctx, "dex", metav1.GetOptions{})
		if err != nil {
			return consoleFixtureAPIError("get Dex Deployment", err)
		}
		if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.AvailableReplicas != 1 {
			return fmt.Errorf("dex Deployment available replicas %d, want 1 (observed generation %d, want %d)", deployment.Status.AvailableReplicas, deployment.Status.ObservedGeneration, deployment.Generation)
		}
		return consoleFixtureDiscovery(ctx, httpClient, fixture.IssuerURL)
	})
	if err != nil {
		return fixture, err
	}
	ginkgo.GinkgoLogr.Info("Configuring fixture ExternalAuth", "name", fixture.Namespace)
	_, err = CreateOrUpdateExternalAuthAndWait20240610(ctx, factory.NewExternalAuthsClient(), resourceGroup, clusterName, fixture.Namespace, objects.externalAuth, timeout)
	if err != nil {
		return fixture, consoleFixtureAPIError("configure Console ExternalAuth through RP", err)
	}
	return fixture, nil
}

// ConsoleFixtureDiagnostics returns a sanitized Dex status summary without
// condition messages, logs, Secrets, configuration, or full objects. Collection
// takes at most 20 seconds and respects ctx cancellation. After a setup/verifier
// timeout, pass its still-live parent context rather than the expired context.
func ConsoleFixtureDiagnostics(ctx context.Context, config *rest.Config, namespace string) string {
	if ctx.Err() != nil {
		return "Dex diagnostics skipped: parent context canceled"
	}
	if config == nil || namespace == "" {
		return "Dex diagnostics unavailable: admin config and namespace required"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	config = rest.CopyConfig(config)
	config.Timeout = 5 * time.Second
	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		return consoleFixtureAPIError("construct Dex diagnostic client", err).Error()
	}
	return consoleFixtureDiagnostics(ctx, kube, namespace)
}

func consoleFixtureDiagnostics(ctx context.Context, kube kubernetes.Interface, namespace string) string {
	lines := []string{fmt.Sprintf("Dex namespace=%q", namespace)}
	deployment, err := kube.AppsV1().Deployments(namespace).Get(ctx, "dex", metav1.GetOptions{})
	if err != nil {
		lines = append(lines, consoleFixtureAPIError("get Dex Deployment diagnostics", err).Error())
	} else {
		lines = append(lines, fmt.Sprintf("Deployment/dex generation=%d observed=%d replicas=%d ready=%d available=%d", deployment.Generation, deployment.Status.ObservedGeneration, deployment.Status.Replicas, deployment.Status.ReadyReplicas, deployment.Status.AvailableReplicas))
		for _, c := range deployment.Status.Conditions {
			lines = append(lines, fmt.Sprintf("Deployment/dex condition=%q status=%q reason=%q", c.Type, c.Status, c.Reason))
		}
	}
	selector := metav1.ListOptions{LabelSelector: "app=console-login-dex"}
	replicaSets, err := kube.AppsV1().ReplicaSets(namespace).List(ctx, selector)
	if err != nil {
		lines = append(lines, consoleFixtureAPIError("list Dex ReplicaSet diagnostics", err).Error())
	} else {
		lines = append(lines, fmt.Sprintf("Dex ReplicaSets=%d", len(replicaSets.Items)))
		for _, rs := range replicaSets.Items {
			for _, c := range rs.Status.Conditions {
				lines = append(lines, fmt.Sprintf("ReplicaSet/%s condition=%q status=%q reason=%q", rs.Name, c.Type, c.Status, c.Reason))
			}
		}
	}
	pods, err := kube.CoreV1().Pods(namespace).List(ctx, selector)
	if err != nil {
		lines = append(lines, consoleFixtureAPIError("list Dex Pod diagnostics", err).Error())
	} else {
		lines = append(lines, fmt.Sprintf("Dex Pods=%d", len(pods.Items)))
		for _, pod := range pods.Items {
			lines = append(lines, fmt.Sprintf("Pod/%s phase=%q reason=%q", pod.Name, pod.Status.Phase, pod.Status.Reason))
			for _, c := range pod.Status.Conditions {
				lines = append(lines, fmt.Sprintf("Pod/%s condition=%q status=%q reason=%q", pod.Name, c.Type, c.Status, c.Reason))
			}
			for _, container := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
				prefix := fmt.Sprintf("Pod/%s container=%q ready=%t restarts=%d", pod.Name, container.Name, container.Ready, container.RestartCount)
				lines = append(lines, prefix)
				if s := container.State.Waiting; s != nil {
					lines = append(lines, fmt.Sprintf("%s waiting=%q", prefix, s.Reason))
				}
				if s := container.State.Terminated; s != nil {
					lines = append(lines, fmt.Sprintf("%s terminated=%q exitCode=%d", prefix, s.Reason, s.ExitCode))
				}
				if s := container.LastTerminationState.Terminated; s != nil {
					lines = append(lines, fmt.Sprintf("%s lastTerminated=%q exitCode=%d", prefix, s.Reason, s.ExitCode))
				}
			}
		}
	}
	slices.Sort(lines[1:])
	return strings.Join(lines, "; ")
}

func validateConsoleFixtureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("ARM-published Console URL must be an HTTPS origin without credentials, query, or fragment")
	}
	return nil
}

// API validation errors can echo submitted Secret values. Report only their
// classification, never their message, body, or the submitted object.
func consoleFixtureAPIError(action string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", action, context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", action, context.Canceled)
	}
	var response *azcore.ResponseError
	if errors.As(err, &response) {
		return fmt.Errorf("%s failed (ARM HTTP status %d)", action, response.StatusCode)
	}
	return fmt.Errorf("%s failed (%T, reason %s)", action, err, apierrors.ReasonForError(err))
}

func waitConsoleFixture(ctx context.Context, description string, check func(context.Context) error) error {
	started := time.Now()
	var last string
	err := wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := check(ctx); err != nil {
			if observed := err.Error(); observed != last {
				last = observed
				ginkgo.GinkgoLogr.Info("waiting for Console fixture", "expected", description, "observed", last)
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s after %s: %w; last observation: %s", description, time.Since(started).Round(time.Second), err, last)
	}
	ginkgo.GinkgoLogr.Info("Console fixture ready", "phase", description, "elapsed", time.Since(started).Round(time.Second))
	return nil
}

func consoleFixtureDiscovery(ctx context.Context, client *http.Client, issuer string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return fmt.Errorf("create Dex discovery request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dex HTTPS discovery failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dex discovery HTTP status %d, want 200", resp.StatusCode)
	}
	var discovery struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery); err != nil {
		return fmt.Errorf("dex discovery did not return a valid JSON document")
	}
	if discovery.Issuer != issuer {
		return fmt.Errorf("dex discovery issuer does not match configured issuer")
	}
	return nil
}

type consoleFixtureObjects struct {
	dexSecret, consoleSecret *corev1.Secret
	service                  *corev1.Service
	deployment               *appsv1.Deployment
	marker                   *corev1.ConfigMap
	role                     *rbacv1.Role
	binding                  *rbacv1.RoleBinding
	externalAuth             hcpsdk20240610preview.ExternalAuth
}

func newConsoleFixtureObjects(f ConsoleFixture, clientSecret string, cert, key []byte) (*consoleFixtureObjects, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(f.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash Dex login password")
	}
	config, err := json.Marshal(map[string]any{
		"issuer":           f.IssuerURL,
		"storage":          map[string]any{"type": "sqlite3", "config": map[string]string{"file": "/var/dex/dex.db"}},
		"web":              map[string]string{"https": "0.0.0.0:5556", "tlsCert": "/etc/dex/tls.crt", "tlsKey": "/etc/dex/tls.key"},
		"logger":           map[string]string{"level": "error", "format": "json"},
		"oauth2":           map[string]any{"responseTypes": []string{"code"}, "skipApprovalScreen": true},
		"enablePasswordDB": true,
		"staticPasswords":  []map[string]string{{"email": f.Username, "hash": string(hash), "username": f.Username, "userID": f.Namespace}},
		"staticClients":    []map[string]any{{"id": f.Namespace, "secret": clientSecret, "name": "OpenShift Console", "public": false, "redirectURIs": []string{strings.TrimSuffix(f.ConsoleURL, "/") + "/auth/callback"}}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode Dex configuration")
	}
	meta := metav1.ObjectMeta{Name: "dex", Namespace: f.Namespace}
	labels := map[string]string{"app": "console-login-dex"}
	o := &consoleFixtureObjects{
		dexSecret:     &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{"config.json": config, "tls.crt": cert, "tls.key": key}},
		consoleSecret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: f.Namespace + "-console-openshift-console", Namespace: "openshift-config"}, Data: map[string][]byte{"clientSecret": []byte(clientSecret)}},
		service:       &corev1.Service{ObjectMeta: meta, Spec: corev1.ServiceSpec{Selector: labels, Ports: []corev1.ServicePort{{Name: "https", Port: 5556, TargetPort: intstr.FromString("https")}}}},
		marker:        &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: f.ConfigMapName, Namespace: f.Namespace}, Data: map[string]string{"marker": f.Marker}},
		role:          &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "marker-reader", Namespace: f.Namespace}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{f.ConfigMapName}, Verbs: []string{"get"}}}},
		binding:       &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "marker-reader", Namespace: f.Namespace}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "marker-reader"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: f.Namespace + ":" + f.Username}}},
	}
	o.deployment = &appsv1.Deployment{
		ObjectMeta: meta,
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)), Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}, Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{
				AutomountServiceAccountToken: ptr.To(false),
				// Let restricted-v2 SCC assign the UID and fsGroup, including
				// writable group ownership for the SQLite emptyDir.
				SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				Containers: []corev1.Container{{
					Name: "dex", Image: consoleDexImage, Command: []string{"/usr/local/bin/dex"}, Args: []string{"serve", "/etc/dex/config.json"},
					Ports:           []corev1.ContainerPort{{Name: "https", ContainerPort: 5556}},
					SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false), ReadOnlyRootFilesystem: ptr.To(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}, Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")}},
					ReadinessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("https")}}, PeriodSeconds: 5},
					VolumeMounts:    []corev1.VolumeMount{{Name: "config", MountPath: "/etc/dex", ReadOnly: true}, {Name: "data", MountPath: "/var/dex"}},
				}},
				Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "dex", DefaultMode: ptr.To(int32(0444))}}}, {Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptr.To(resource.MustParse("128Mi"))}}}},
			}},
		},
	}
	o.externalAuth = hcpsdk20240610preview.ExternalAuth{Properties: &hcpsdk20240610preview.ExternalAuthProperties{
		Issuer: &hcpsdk20240610preview.TokenIssuerProfile{URL: ptr.To(f.IssuerURL), CA: ptr.To(string(f.IssuerCA)), Audiences: []*string{ptr.To(f.Namespace)}},
		Claim: &hcpsdk20240610preview.ExternalAuthClaimProfile{Mappings: &hcpsdk20240610preview.TokenClaimMappingsProfile{
			Username: &hcpsdk20240610preview.UsernameClaimProfile{Claim: ptr.To("email"), PrefixPolicy: ptr.To(hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix), Prefix: ptr.To(f.Namespace + ":")},
			Groups:   &hcpsdk20240610preview.GroupClaimProfile{Claim: ptr.To("groups")},
		}},
		Clients: []*hcpsdk20240610preview.ExternalAuthClientProfile{{ClientID: ptr.To(f.Namespace), Type: ptr.To(hcpsdk20240610preview.ExternalAuthClientTypeConfidential), ExtraScopes: []*string{ptr.To("email")}, Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{Name: ptr.To("console"), AuthClientNamespace: ptr.To("openshift-console")}}},
	}}
	return o, nil
}

func consoleFixtureCertificates(host string, now time.Time) (caPEM, certPEM, keyPEM []byte, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Console test Dex CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	leaf := &x509.Certificate{SerialNumber: new(big.Int).Add(serial, big.NewInt(1)), DNSNames: []string{host}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}
