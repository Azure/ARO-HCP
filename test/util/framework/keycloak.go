// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

const (
	keycloakTestImage                 = "quay.io/keycloak/keycloak@sha256:9409c59bdfb65dbffa20b11e6f18b8abb9281d480c7ca402f51ed3d5977e6007"
	openshiftConsoleSessionCookieName = "openshift-session-token"
)

type KeycloakTestProviderOptions struct {
	Realm                  string
	Username               string
	Password               string
	Group                  string
	ConsoleClientID        string
	ConsoleClientSecret    string
	ConsoleRedirectURI     string
	ConsoleWebOrigin       string
	CommandLineClientID    string
	BootstrapAdminUser     string
	BootstrapAdminPassword string
}

type KeycloakTestProvider struct {
	Namespace             string
	IssuerURL             string
	AuthorizationEndpoint string
	TokenEndpoint         string
}

type BrowserLoginResult struct {
	AuthorizationRequest *url.URL
	FinalURL             *url.URL
	ConsoleSessionCookie *http.Cookie
}

type keycloakRealm struct {
	Realm                 string           `json:"realm"`
	Enabled               bool             `json:"enabled"`
	SSLRequired           string           `json:"sslRequired"`
	LoginWithEmailAllowed bool             `json:"loginWithEmailAllowed"`
	RegistrationAllowed   bool             `json:"registrationAllowed"`
	ResetPasswordAllowed  bool             `json:"resetPasswordAllowed"`
	RememberMe            bool             `json:"rememberMe"`
	Groups                []keycloakGroup  `json:"groups"`
	Users                 []keycloakUser   `json:"users"`
	Clients               []keycloakClient `json:"clients"`
}

type keycloakGroup struct {
	Name string `json:"name"`
}

type keycloakUser struct {
	Username      string               `json:"username"`
	Enabled       bool                 `json:"enabled"`
	EmailVerified bool                 `json:"emailVerified"`
	FirstName     string               `json:"firstName"`
	LastName      string               `json:"lastName"`
	Email         string               `json:"email"`
	Groups        []string             `json:"groups"`
	Credentials   []keycloakCredential `json:"credentials"`
}

type keycloakCredential struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

type keycloakClient struct {
	ClientID                  string                   `json:"clientId"`
	Enabled                   bool                     `json:"enabled"`
	Protocol                  string                   `json:"protocol"`
	PublicClient              bool                     `json:"publicClient"`
	Secret                    string                   `json:"secret,omitempty"`
	StandardFlowEnabled       bool                     `json:"standardFlowEnabled"`
	DirectAccessGrantsEnabled bool                     `json:"directAccessGrantsEnabled"`
	DefaultClientScopes       []string                 `json:"defaultClientScopes"`
	RedirectURIs              []string                 `json:"redirectUris,omitempty"`
	WebOrigins                []string                 `json:"webOrigins,omitempty"`
	ProtocolMappers           []keycloakProtocolMapper `json:"protocolMappers"`
}

type keycloakProtocolMapper struct {
	Name           string            `json:"name"`
	Protocol       string            `json:"protocol"`
	ProtocolMapper string            `json:"protocolMapper"`
	Config         map[string]string `json:"config"`
}

type oidcDiscoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

func DeployKeycloakTestProvider(ctx context.Context, adminRESTConfig *rest.Config, options KeycloakTestProviderOptions) (*KeycloakTestProvider, error) {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic Kubernetes client: %w", err)
	}

	namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-keycloak-"},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak namespace: %w", err)
	}

	const name = "keycloak"
	labels := map[string]string{"app": name}
	_, err = kubeClient.CoreV1().Services(namespace.Name).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       8080,
				TargetPort: intstr.FromString("http"),
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak service: %w", err)
	}

	routeGVR := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	route, err := dynamicClient.Resource(routeGVR).Namespace(namespace.Name).Create(ctx, &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"to": map[string]any{
					"kind": "Service",
					"name": name,
				},
				"port": map[string]any{
					"targetPort": "http",
				},
				"tls": map[string]any{
					"termination":                   "edge",
					"insecureEdgeTerminationPolicy": "Redirect",
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak route: %w", err)
	}

	var routeHost string
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := dynamicClient.Resource(routeGVR).Namespace(namespace.Name).Get(ctx, route.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		routeHost, _, _ = unstructured.NestedString(current.Object, "spec", "host")
		return routeHost != "", nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for Keycloak route host: %w", err)
	}

	issuerURL := fmt.Sprintf("https://%s/realms/%s", routeHost, url.PathEscape(options.Realm))
	realmJSON, err := json.Marshal(buildKeycloakRealm(options))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Keycloak realm: %w", err)
	}

	_, err = kubeClient.CoreV1().Secrets(namespace.Name).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "keycloak-bootstrap"},
		StringData: map[string]string{
			"admin-username": options.BootstrapAdminUser,
			"admin-password": options.BootstrapAdminPassword,
			"realm.json":     string(realmJSON),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak bootstrap secret: %w", err)
	}

	_, err = kubeClient.AppsV1().Deployments(namespace.Name).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  name,
						Image: keycloakTestImage,
						Args:  []string{"start-dev", "--import-realm"},
						Env: []corev1.EnvVar{
							{Name: "KC_BOOTSTRAP_ADMIN_USERNAME", ValueFrom: secretKeyRef("keycloak-bootstrap", "admin-username")},
							{Name: "KC_BOOTSTRAP_ADMIN_PASSWORD", ValueFrom: secretKeyRef("keycloak-bootstrap", "admin-password")},
							{Name: "KC_HOSTNAME", Value: "https://" + routeHost},
							{Name: "KC_HTTP_ENABLED", Value: "true"},
							{Name: "KC_PROXY_HEADERS", Value: "xforwarded"},
							{Name: "KC_HEALTH_ENABLED", Value: "true"},
						},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 8080},
							{Name: "management", ContainerPort: 9000},
						},
						StartupProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/health/started",
								Port: intstr.FromString("management"),
							}},
							PeriodSeconds:    5,
							FailureThreshold: 60,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/health/ready",
								Port: intstr.FromString("management"),
							}},
							PeriodSeconds:    5,
							FailureThreshold: 12,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
							SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "realm-import",
							MountPath: "/opt/keycloak/data/import",
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "realm-import",
						VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: "keycloak-bootstrap",
							Items: []corev1.KeyToPath{{
								Key:  "realm.json",
								Path: "realm.json",
							}},
						}},
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak deployment: %w", err)
	}

	discoveryURL := issuerURL + "/.well-known/openid-configuration"
	var discovery oidcDiscoveryDocument
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		discovery, lastErr = getOIDCDiscoveryDocument(ctx, discoveryURL)
		return lastErr == nil && discovery.Issuer == issuerURL, nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("timed out waiting for Keycloak discovery endpoint: %w", lastErr)
		}
		return nil, fmt.Errorf("timed out waiting for Keycloak discovery endpoint: %w", err)
	}

	return &KeycloakTestProvider{
		Namespace:             namespace.Name,
		IssuerURL:             issuerURL,
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
	}, nil
}

func RequestKeycloakPasswordToken(ctx context.Context, provider *KeycloakTestProvider, clientID, username, password string) (string, error) {
	values := url.Values{
		"grant_type": {"password"},
		"client_id":  {clientID},
		"username":   {username},
		"password":   {password},
		"scope":      {"openid profile"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to build Keycloak token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := keycloakHTTPClient(nil).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request Keycloak token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read Keycloak token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloak token endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token oidcTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("failed to decode Keycloak token response: %w", err)
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return "", fmt.Errorf("keycloak token response did not contain a bearer access token")
	}
	return token.AccessToken, nil
}

func CheckConsoleLoginRedirect(ctx context.Context, consoleLoginURL, expectedAuthorizationEndpoint string) error {
	client := keycloakHTTPClient(nil)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, consoleLoginURL, nil)
	if err != nil {
		return fmt.Errorf("failed to build console login readiness request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to request console login endpoint: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("console login endpoint returned HTTP %d; expected HTTP %d redirect to %q", resp.StatusCode, http.StatusFound, expectedAuthorizationEndpoint)
	}
	location, err := resp.Location()
	if err != nil {
		return fmt.Errorf("console login redirect did not contain a valid Location header: %w", err)
	}
	expectedURL, err := url.Parse(expectedAuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse expected Keycloak authorization endpoint: %w", err)
	}
	if !strings.EqualFold(location.Scheme, expectedURL.Scheme) ||
		!strings.EqualFold(location.Host, expectedURL.Host) ||
		location.Path != expectedURL.Path {
		return fmt.Errorf("console login endpoint redirected to %q; expected Keycloak authorization endpoint %q", location.String(), expectedAuthorizationEndpoint)
	}
	return nil
}

func CompleteKeycloakConsoleLogin(ctx context.Context, consoleLoginURL, keycloakIssuerURL, username, password string) (*BrowserLoginResult, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create browser cookie jar: %w", err)
	}

	var authorizationRequest *url.URL
	client := keycloakHTTPClient(jar)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), keycloakIssuerURL+"/protocol/openid-connect/auth") {
			copied := *req.URL
			authorizationRequest = &copied
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, consoleLoginURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build console login request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to open console login flow: %w", err)
	}
	loginFormURL, err := findKeycloakLoginFormAction(resp)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if authorizationRequest == nil {
		return nil, fmt.Errorf("console login did not redirect to the Keycloak authorization endpoint")
	}

	form := url.Values{
		"username": {username},
		"password": {password},
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, loginFormURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to build Keycloak login form request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to submit Keycloak login form: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("console login completed with HTTP %d", resp.StatusCode)
	}
	consoleURL, err := url.Parse(consoleLoginURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse console login URL: %w", err)
	}
	if !strings.EqualFold(resp.Request.URL.Host, consoleURL.Host) {
		return nil, fmt.Errorf("console login ended at %q instead of console host %q", resp.Request.URL.Host, consoleURL.Host)
	}
	consoleSessionCookie := findCookieWithPrefix(jar.Cookies(consoleURL), openshiftConsoleSessionCookieName)
	if consoleSessionCookie == nil {
		return nil, fmt.Errorf("console login completed without establishing an %q session cookie", openshiftConsoleSessionCookieName)
	}

	return &BrowserLoginResult{
		AuthorizationRequest: authorizationRequest,
		FinalURL:             resp.Request.URL,
		ConsoleSessionCookie: consoleSessionCookie,
	}, nil
}

func findCookieWithPrefix(cookies []*http.Cookie, prefix string) *http.Cookie {
	for _, cookie := range cookies {
		if (cookie.Name == prefix || strings.HasPrefix(cookie.Name, prefix+"-")) && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func buildKeycloakRealm(options KeycloakTestProviderOptions) keycloakRealm {
	defaultClientScopes := []string{"web-origins", "acr", "roles", "profile", "basic", "email"}
	clientMappers := func(audience string) []keycloakProtocolMapper {
		return []keycloakProtocolMapper{
			{
				Name:           "audience",
				Protocol:       "openid-connect",
				ProtocolMapper: "oidc-audience-mapper",
				Config: map[string]string{
					"included.client.audience": audience,
					"access.token.claim":       "true",
					"id.token.claim":           "true",
				},
			},
			{
				Name:           "groups",
				Protocol:       "openid-connect",
				ProtocolMapper: "oidc-group-membership-mapper",
				Config: map[string]string{
					"claim.name":           "groups",
					"full.path":            "false",
					"access.token.claim":   "true",
					"id.token.claim":       "true",
					"userinfo.token.claim": "true",
				},
			},
		}
	}

	return keycloakRealm{
		Realm:                 options.Realm,
		Enabled:               true,
		SSLRequired:           "external",
		LoginWithEmailAllowed: true,
		Groups:                []keycloakGroup{{Name: options.Group}},
		Users: []keycloakUser{{
			Username:      options.Username,
			Enabled:       true,
			EmailVerified: true,
			FirstName:     "E2E",
			LastName:      "User",
			Email:         options.Username + "@example.com",
			Groups:        []string{"/" + options.Group},
			Credentials: []keycloakCredential{{
				Type:      "password",
				Value:     options.Password,
				Temporary: false,
			}},
		}},
		Clients: []keycloakClient{
			{
				ClientID:                  options.ConsoleClientID,
				Enabled:                   true,
				Protocol:                  "openid-connect",
				PublicClient:              false,
				Secret:                    options.ConsoleClientSecret,
				StandardFlowEnabled:       true,
				DirectAccessGrantsEnabled: false,
				DefaultClientScopes:       defaultClientScopes,
				RedirectURIs:              []string{options.ConsoleRedirectURI},
				WebOrigins:                []string{options.ConsoleWebOrigin},
				ProtocolMappers:           clientMappers(options.ConsoleClientID),
			},
			{
				ClientID:                  options.CommandLineClientID,
				Enabled:                   true,
				Protocol:                  "openid-connect",
				PublicClient:              true,
				StandardFlowEnabled:       true,
				DirectAccessGrantsEnabled: true,
				DefaultClientScopes:       defaultClientScopes,
				RedirectURIs:              []string{"http://localhost:8000"},
				ProtocolMappers:           clientMappers(options.CommandLineClientID),
			},
		},
	}
}

func secretKeyRef(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name},
		Key:                  key,
	}}
}

func getOIDCDiscoveryDocument(ctx context.Context, discoveryURL string) (oidcDiscoveryDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return oidcDiscoveryDocument{}, err
	}
	resp, err := keycloakHTTPClient(nil).Do(req)
	if err != nil {
		return oidcDiscoveryDocument{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oidcDiscoveryDocument{}, fmt.Errorf("discovery endpoint returned HTTP %d", resp.StatusCode)
	}
	var discovery oidcDiscoveryDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery); err != nil {
		return oidcDiscoveryDocument{}, err
	}
	if discovery.Issuer == "" || discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" {
		return oidcDiscoveryDocument{}, fmt.Errorf("discovery document omitted required endpoints")
	}
	return discovery, nil
}

func keycloakHTTPClient(jar http.CookieJar) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // ephemeral OpenShift routes may use development certificates
	}
	return &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   2 * time.Minute,
	}
}

func findKeycloakLoginFormAction(resp *http.Response) (*url.URL, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keycloak login page returned HTTP %d", resp.StatusCode)
	}
	document, err := html.Parse(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Keycloak login page: %w", err)
	}
	var visit func(*html.Node) string
	visit = func(node *html.Node) string {
		if node.Type == html.ElementNode && node.Data == "form" {
			var id, action string
			for _, attr := range node.Attr {
				switch attr.Key {
				case "id":
					id = attr.Val
				case "action":
					action = attr.Val
				}
			}
			if id == "kc-form-login" {
				return action
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if action := visit(child); action != "" {
				return action
			}
		}
		return ""
	}
	action := visit(document)
	if action == "" {
		return nil, fmt.Errorf("keycloak login page did not contain the expected login form")
	}
	formURL, err := url.Parse(action)
	if err != nil {
		return nil, fmt.Errorf("keycloak login form had an invalid action URL: %w", err)
	}
	return resp.Request.URL.ResolveReference(formURL), nil
}
