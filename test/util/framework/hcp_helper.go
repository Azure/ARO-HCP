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

package framework

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/onsi/ginkgo/v2"
	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// checkOperationResult ensures the result model returned by a runtime.Poller
// matches the resource model returned from a GET request.
func checkOperationResult(expectModel, resultModel any) error {
	diff := cmp.Diff(expectModel, resultModel,
		// Add per-model fields that should be ignored in the comparison. For example
		// read-only values that change on their own, or are computed asynchronously
		// and may not be immediately available in the operation result response.
		//
		// SystemData is set synchronously by the frontend (parsed from the ARM
		// system-data header via ensureSystemData), not computed asynchronously by
		// the backend, so it normally matches. Excluding it for every preview API
		// version is defensive parity that also guards against timestamp-based flakes
		// from ensureSystemData's time.Now() fallbacks when the header is absent.
		//
		// Note: I'm anticipating adding "Identity.UserAssignedIdentities" here once
		// the RP takes over fetching client and principal IDs from the Managed Identity
		// service. That would be a concrete example of asynchronously computed fields.
		cmpopts.IgnoreFields(hcpsdk20240610preview.HcpOpenShiftCluster{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20240610preview.NodePool{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20240610preview.ExternalAuth{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20251223preview.HcpOpenShiftCluster{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20251223preview.NodePool{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20251223preview.ExternalAuth{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260630preview.HcpOpenShiftCluster{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260630preview.NodePool{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260630preview.ExternalAuth{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260901preview.HcpOpenShiftCluster{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260901preview.NodePool{}, "SystemData"),
		cmpopts.IgnoreFields(hcpsdk20260901preview.ExternalAuth{}, "SystemData"),
	)

	if len(diff) > 0 {
		return fmt.Errorf("operation result model did not match expected model for type %T:\n%s", resultModel, diff)
	}

	return nil
}

// stateConflictBackoff is the retry config for transient state conflicts (ARO-25884).
var stateConflictBackoff = wait.Backoff{
	Steps:    5,               // up to 5 attempts total
	Duration: 1 * time.Minute, // initial wait before first retry
	Factor:   2.0,             // double the wait each retry (1m, 2m, 4m, 8m)
	Jitter:   0.1,             // ±10% randomization to avoid thundering herd
}

// csStateConflictPattern matches the cluster-service error message format:
// "Cluster '<id>' is in state '<state>', can't update"
var csStateConflictPattern = regexp.MustCompile(`is in state '[^']+', can't update`)

// isTransientDeleteError detects transient write conflicts during cluster deletion.
// Only matches ConflictingConcurrentWriteNotAllowed, which is caused by optimistic
// concurrency control in the backend and resolves on retry.
func isTransientDeleteError(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	return responseError.StatusCode == http.StatusConflict &&
		responseError.ErrorCode == "ConflictingConcurrentWriteNotAllowed"
}

// isTransientUpdateError detects errors worth retrying during cluster updates:
//   - HTTP 500: e.g. Cosmos DB ETag conflict after cluster-service commit
//   - HTTP 409: Conflict
//   - HTTP 400: cluster-service state conflict when cluster is in a transitional state
func isTransientUpdateError(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	if responseError.StatusCode == http.StatusInternalServerError ||
		responseError.StatusCode == http.StatusConflict {
		return true
	}
	if responseError.StatusCode == http.StatusBadRequest &&
		csStateConflictPattern.MatchString(responseError.Error()) {
		return true
	}
	return false
}

type NonConformingClustersError struct {
	clusters []string
}

func (e *NonConformingClustersError) Error() string {
	return fmt.Sprintf("the following clusters did not have tags[%s]=%s: %v; we require end-to-end tests to opt into this tag to ensure that the control planes we provision during automated test runs have minimal footprints on our production infrastructure", metadataapi.TagClusterSizeOverride, coreapi.MinimalControlPlanePodSizing, e.clusters)
}

func CreateClusterRoleBinding(ctx context.Context, subject string, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "entra-admins",
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []v1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     subject,
			},
		},
	}, metav1.CreateOptions{})

	if err != nil {
		return fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	return nil
}

// Helper to generate SSH key pair
func GenerateSSHKeyPair() (publicKey string, privateKey string, err error) {
	// Generate RSA key pair
	privateKeyData, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	// Encode private key to PEM format
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKeyData),
	}
	privateKeyStr := string(pem.EncodeToMemory(privateKeyPEM))

	// Generate public key in SSH format
	pub, err := ssh.NewPublicKey(&privateKeyData.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicKeyStr := string(ssh.MarshalAuthorizedKey(pub))

	return publicKeyStr, privateKeyStr, nil
}

// Helper to generate kubeconfig
func GenerateKubeconfig(restConfig *rest.Config) (string, error) {
	// Create kubeconfig using proper types
	config := clientcmdapi.NewConfig()

	// Define cluster
	clusterName := "cluster"
	cluster := clientcmdapi.NewCluster()
	cluster.Server = restConfig.Host

	// In development environments, CAData is cleared and Insecure is set to true
	// We need to handle this case by adding insecure-skip-tls-verify
	if len(restConfig.CAData) == 0 || restConfig.Insecure {
		cluster.InsecureSkipTLSVerify = true
	} else {
		cluster.CertificateAuthorityData = restConfig.CAData
	}
	config.Clusters[clusterName] = cluster

	// Define user
	userName := "admin"
	authInfo := clientcmdapi.NewAuthInfo()
	// Support both certificate and token authentication
	if restConfig.BearerToken != "" {
		authInfo.Token = restConfig.BearerToken
	} else {
		authInfo.ClientCertificateData = restConfig.CertData
		authInfo.ClientKeyData = restConfig.KeyData
	}
	config.AuthInfos[userName] = authInfo

	// Define context
	contextName := "admin@cluster"
	context := clientcmdapi.NewContext()
	context.Cluster = clusterName
	context.AuthInfo = userName
	config.Contexts[contextName] = context

	// Set current context
	config.CurrentContext = contextName

	// Marshal to YAML
	kubeconfigBytes, err := clientcmd.Write(*config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kubeconfig: %w", err)
	}

	return string(kubeconfigBytes), nil
}

// convertViaJSON converts between structurally identical types from different SDK versions
// by JSON round-tripping. This is necessary because ClusterParams stores the old SDK types
// but we need to produce new SDK types.
func convertViaJSON[T any](src any) (*T, error) {
	b, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal for type conversion: %w", err)
	}
	var dst T
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&dst); err != nil {
		return nil, fmt.Errorf("failed to unmarshal for type conversion: %w", err)
	}
	return &dst, nil
}

func HasNodeLabel(nodes []corev1.Node, key, value string, expectedCount ...int) bool {
	count := 0
	for _, node := range nodes {
		if node.Labels[key] == value {
			count++
		}
	}

	if len(expectedCount) == 0 {
		return count > 0
	}

	return count == expectedCount[0]
}

func HasNodeTaint(nodes []corev1.Node, key, value string, effect corev1.TaintEffect, expectedCount ...int) bool {
	count := 0
	for _, node := range nodes {
		for _, taint := range node.Spec.Taints {
			if taint.Key == key && taint.Value == value && taint.Effect == effect {
				count++
				break
			}
		}
	}

	if len(expectedCount) == 0 {
		return count > 0
	}

	return count == expectedCount[0]
}

// requiredResourceTypesForAPIVersion lists the ARM resource types that must all
// support a given API version for async operations (create/delete/update) to work
// end-to-end. The generated SDK uses the same api-version query parameter for both
// the resource and its operation status polling endpoint.
var requiredResourceTypesForAPIVersion = []string{
	"hcpOpenShiftClusters",
	"locations/hcpOperationStatuses",
	"locations/hcpOperationResults",
}

// IsHCPAPIVersionAvailable checks whether apiVersion is registered in the ARM
// provider manifest for all resource types required by the ARO-HCP SDK. In
// development environments the check is skipped (always returns true).
func (tc *perItOrDescribeTestContext) IsHCPAPIVersionAvailable(ctx context.Context, apiVersion string) (bool, error) {
	if tc.perBinaryInvocationTestContext.isDevelopmentEnvironment {
		return true, nil
	}
	factory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get ARM resources client factory: %w", err)
	}
	provider, err := factory.NewProvidersClient().Get(ctx, "Microsoft.RedHatOpenShift", nil)
	if err != nil {
		return false, fmt.Errorf("failed to get Microsoft.RedHatOpenShift resource provider: %w", err)
	}
	for _, requiredRT := range requiredResourceTypesForAPIVersion {
		found := false
		for _, rt := range provider.ResourceTypes {
			if rt.ResourceType == nil || !strings.EqualFold(*rt.ResourceType, requiredRT) {
				continue
			}
			for _, v := range rt.APIVersions {
				if v != nil && strings.EqualFold(*v, apiVersion) {
					found = true
					break
				}
			}
		}
		if !found {
			ginkgo.GinkgoLogr.Info("API version not available for resource type",
				"apiVersion", apiVersion, "resourceType", requiredRT)
			return false, nil
		}
	}
	return true, nil
}

// GetTestRunnerPublicIP returns the public IP address of the test runner by
// querying a public IP echo service. The IP can be used to add the test
// runner to an HCP cluster's authorized CIDR list so that framework helpers
// can reach the Kubernetes API server directly.
func GetTestRunnerPublicIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://checkip.amazonaws.com", nil)
	if err != nil {
		return "", fmt.Errorf("failed to build public IP echo request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query public IP echo service: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read public IP echo response: %w", err)
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("public IP echo service returned invalid IP %q", ip)
	}
	return ip, nil
}
