package verifiers

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

type verifyIngressDefaultCertificate struct {
	managementConfig *rest.Config
	credential       azcore.TokenCredential
	resourceID       string
	timeout          time.Duration
}

func VerifyIngressDefaultCertificate(managementConfig *rest.Config, credential azcore.TokenCredential, resourceID string, timeout time.Duration) HostedClusterVerifier {
	return verifyIngressDefaultCertificate{managementConfig: managementConfig, credential: credential, resourceID: resourceID, timeout: timeout}
}

func (verifier verifyIngressDefaultCertificate) Name() string {
	return "VerifyIngressDefaultCertificate"
}

func (verifier verifyIngressDefaultCertificate) Verify(ctx context.Context, adminConfig *rest.Config) error {
	managementClient, err := dynamic.NewForConfig(verifier.managementConfig)
	if err != nil {
		return fmt.Errorf("create management client: %w", err)
	}
	guestClient, err := operatorclient.NewForConfig(adminConfig)
	if err != nil {
		return fmt.Errorf("create guest operator client: %w", err)
	}
	return pollUntilReady(ctx, verifier.Name(), verifier.timeout, DefaultPollInterval, adminConfig, 0, nil, func(ctx context.Context) error {
		hostedClusters, err := managementClient.Resource(schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"}).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("find created HostedCluster: %w", err)
		}
		var hostedCluster *unstructured.Unstructured
		for index := range hostedClusters.Items {
			candidate := &hostedClusters.Items[index]
			if strings.EqualFold(candidate.GetAnnotations()[hyperv1.ManagedAzureResourceIDAnnotation], verifier.resourceID) {
				if hostedCluster != nil {
					return fmt.Errorf("multiple HostedClusters match %s", verifier.resourceID)
				}
				hostedCluster = candidate
			}
		}
		if hostedCluster == nil {
			return fmt.Errorf("no HostedCluster matches %s", verifier.resourceID)
		}
		secretName, _, err := unstructured.NestedString(hostedCluster.Object, "spec", "operatorConfiguration", "ingressOperator", "defaultCertificate", "name")
		if err != nil || secretName == "" {
			return fmt.Errorf("HostedCluster %s/%s has no default ingress certificate reference", hostedCluster.GetNamespace(), hostedCluster.GetName())
		}
		provider, err := managementClient.Resource(schema.GroupVersionResource{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Resource: "secretproviderclasses"}).Namespace(hostedCluster.GetNamespace()).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get ingress certificate Key Vault source: %w", err)
		}
		parameters, _, err := unstructured.NestedStringMap(provider.Object, "spec", "parameters")
		if err != nil {
			return fmt.Errorf("read Key Vault source parameters: %w", err)
		}
		if parameters["keyvaultName"] == "" || (parameters["cloudName"] != "" && parameters["cloudName"] != "AzurePublicCloud") {
			return fmt.Errorf("expected a public Azure Key Vault source")
		}
		var objects struct {
			Array []string `json:"array"`
		}
		if err := yaml.Unmarshal([]byte(parameters["objects"]), &objects); err != nil {
			return fmt.Errorf("decode Key Vault objects: %w", err)
		}
		if len(objects.Array) != 1 {
			return fmt.Errorf("expected one ingress certificate source, got %d", len(objects.Array))
		}
		var object struct {
			ObjectName    string `json:"objectName"`
			ObjectType    string `json:"objectType"`
			ObjectVersion string `json:"objectVersion"`
		}
		if err := yaml.Unmarshal([]byte(objects.Array[0]), &object); err != nil {
			return fmt.Errorf("decode ingress certificate source: %w", err)
		}
		if object.ObjectName == "" || object.ObjectType != "secret" {
			return fmt.Errorf("expected named Key Vault certificate secret source")
		}
		certificateClient, err := azcertificates.NewClient("https://"+parameters["keyvaultName"]+".vault.azure.net", verifier.credential, nil)
		if err != nil {
			return fmt.Errorf("create Key Vault certificate client: %w", err)
		}
		certificate, err := certificateClient.GetCertificate(ctx, object.ObjectName, object.ObjectVersion, nil)
		if err != nil {
			return fmt.Errorf("get intended Key Vault certificate %s: %w", object.ObjectName, err)
		}
		if certificate.Policy == nil || certificate.Policy.IssuerParameters == nil || certificate.Policy.IssuerParameters.Name == nil || *certificate.Policy.IssuerParameters.Name != "OneCertV2-PublicCA" {
			return fmt.Errorf("Key Vault certificate %s must use OneCertV2-PublicCA, not a self-signed issuer", object.ObjectName)
		}
		ingress, err := guestClient.OperatorV1().IngressControllers("openshift-ingress-operator").Get(ctx, "default", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get guest default IngressController: %w", err)
		}
		if ingress.Status.Domain == "" {
			return fmt.Errorf("default IngressController has no domain")
		}
		host := "certificate-check." + ingress.Status.Domain
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
		if err != nil {
			return fmt.Errorf("verify ingress TLS chain and hostname %s: %w", host, err)
		}
		defer connection.Close()
		state := connection.(*tls.Conn).ConnectionState()
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return fmt.Errorf("ingress TLS handshake returned no verified certificate chain")
		}
		if !bytes.Equal(state.PeerCertificates[0].Raw, certificate.CER) {
			return fmt.Errorf("served ingress leaf does not match Key Vault certificate %s", object.ObjectName)
		}
		return nil
	})
}
