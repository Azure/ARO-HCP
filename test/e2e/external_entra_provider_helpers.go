package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azidentity "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// ---------- Shared types ----------
type providerEntraSecretOut struct {
	TenantID     string    `json:"tenant_id"`
	AppObjectID  string    `json:"app_object_id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	DisplayName  string    `json:"display_name"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"secret_expires_at"`
}

type providerAppWeb struct {
	RedirectUris []string `json:"redirectUris"`
}

// ---------- Microsoft Graph helpers (Azure CLI auth) ----------
func providerGraphToken(ctx context.Context) (string, error) {
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return "", fmt.Errorf("AzureCLICredential: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("get Graph token: %w", err)
	}
	return tok.Token, nil
}

func providerGraphReq[T any](ctx context.Context, method, url, token string, in any, out *T, want int) error {
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return err
		}
		body = &buf
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, body)
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: got %d want %d; body=%s", method, url, resp.StatusCode, want, string(b))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode body: %w; body=%s", err, string(b))
		}
	}
	return nil
}

func providerCreateSecurityGroup(ctx context.Context, token, displayName string) (string, error) {
	body := map[string]any{
		"displayName":     displayName,
		"description":     "E2E external-auth allow group",
		"mailEnabled":     false,
		"mailNickname":    strings.ReplaceAll(strings.ToLower(displayName), " ", "-"),
		"securityEnabled": true,
	}
	var out struct {
		ID string `json:"id"`
	}
	err := providerGraphReq(ctx, http.MethodPost, "https://graph.microsoft.com/v1.0/groups", token, body, &out, http.StatusCreated)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("group create returned empty id")
	}
	return out.ID, nil
}

func providerPatchAppRedirectUris(ctx context.Context, token, appObjectID string, uris []string) error {
	body := map[string]any{
		"web": providerAppWeb{RedirectUris: uris},
	}
	return providerGraphReq[any](ctx, http.MethodPatch,
		"https://graph.microsoft.com/v1.0/applications/"+appObjectID,
		token, body, nil, http.StatusNoContent)
}

// ---------- Kube & port-forward helpers ----------
func providerRun(ctx context.Context, name string, args ...string) (string, error) {
	if name == "oc" || name == "kubectl" || name == "ocm" {
		args = append(args, "--insecure-skip-tls-verify")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func providerFindServiceNamespace(ctx context.Context, svcName string) (string, error) {
	out, err := providerRun(ctx, "kubectl", "get", "svc", "-A",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.metadata.namespace}{"\n"}{end}`)
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(ln), "\t")
		if len(parts) == 2 && parts[0] == svcName {
			return parts[1], nil
		}
	}
	return "", fmt.Errorf("service %q not found in any namespace", svcName)
}

func providerWithPortForward(ctx context.Context, ns, svc string, remotePort, localPort int, f func(baseURL string)) error {
	if localPort == 0 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		localPort = l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
	}

	args := []string{
		"-n", ns, "port-forward", "svc/" + svc,
		fmt.Sprintf("%d:%d", localPort, remotePort),
		"--insecure-skip-tls-verify",
		"--address", "127.0.0.1",
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stderr := &bytes.Buffer{}
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("port-forward start error: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	deadline := time.Now().Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("port-forward didn't open %s: %v; stderr: %s", addr, lastErr, stderr.String())
	}
	defer func() { _ = cmd.Process.Kill() }()

	f("https://" + addr)
	return nil
}

func providerHTTPClient() *http.Client {
	insecure := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

func providerUseKubeconfig(path string) {
	if path != "" {
		_ = os.Setenv("KUBECONFIG", path)
	}
}

func providerGetConsoleRouteHost(ctx context.Context) (string, error) {
	host, err := providerRun(ctx, "kubectl", "-n", "openshift-console",
		"get", "route", "console",
		"-o", "jsonpath={.spec.host}")
	if err != nil || host == "" {
		routes, _ := providerRun(ctx, "kubectl", "-n", "openshift-console", "get", "routes")
		if host == "" {
			return "", fmt.Errorf("failed to get console route host via jsonpath; routes:\n%s", routes)
		}
		return "", fmt.Errorf("failed to get console route host: %v", err)
	}
	return strings.TrimSpace(host), nil
}

// ---------- Clusters-Service breakglass kubeconfig ----------
func providerFetchBreakglassKubeconfig(ctx context.Context, clusterName string) ([]byte, error) {
	csNS, err := providerFindServiceNamespace(ctx, "clusters-service")
	if err != nil {
		return nil, fmt.Errorf("find clusters-service ns: %w", err)
	}
	const csPort = 8000
	csPathAdminKC := fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s/admin_kubeconfig", clusterName)

	var kc []byte
	err = providerWithPortForward(ctx, csNS, "clusters-service", csPort, 0, func(baseURL string) {
		u := baseURL + csPathAdminKC
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err2 := providerHTTPClient().Do(req)
		if err2 != nil {
			err = fmt.Errorf("GET %s: %w", u, err2)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			err = fmt.Errorf("GET %s: %d body=%s", u, resp.StatusCode, string(b))
			return
		}
		kc, err = io.ReadAll(resp.Body)
	})
	if err != nil {
		return nil, err
	}
	if len(kc) == 0 {
		return nil, fmt.Errorf("empty kubeconfig from CS")
	}
	return kc, nil
}
