package holmes

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/ARO-HCP/internal/certs"
	"github.com/Azure/ARO-HCP/internal/csrminting"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"

	hypershiftscheme "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
)

const (
	defaultRSAKeySize  = 2048
	csrTimeout         = 60 * time.Second
	rootCASecret = "root-ca"
	diagnosticsUser    = "aro-diagnostics"
)

type KubeconfigResult struct {
	KubeconfigYAML []byte
	Cleanup        func()
}

type MgmtClusterClientFactory interface {
	GetRESTConfig(ctx context.Context, mgmtClusterResourceID string, credential azcore.TokenCredential) (*rest.Config, error)
}

type defaultMgmtClusterClientFactory struct{}

func (f *defaultMgmtClusterClientFactory) GetRESTConfig(ctx context.Context, mgmtClusterResourceID string, credential azcore.TokenCredential) (*rest.Config, error) {
	return mc.GetAKSRESTConfig(ctx, mgmtClusterResourceID, credential)
}

type KubeconfigBuilder struct {
	mgmtClientFactory MgmtClusterClientFactory
}

func NewKubeconfigBuilder() *KubeconfigBuilder {
	return &KubeconfigBuilder{
		mgmtClientFactory: &defaultMgmtClusterClientFactory{},
	}
}

func (kb *KubeconfigBuilder) BuildDataplaneKubeconfig(
	ctx context.Context,
	credential azcore.TokenCredential,
	mgmtClusterResourceID string,
	hcpNamespace string,
	clusterID string,
	kasEndpoint string,
) (*KubeconfigResult, error) {
	mgmtRESTConfig, err := kb.mgmtClientFactory.GetRESTConfig(ctx, mgmtClusterResourceID, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to get management cluster REST config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(mgmtRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ctrlClient, err := client.New(mgmtRESTConfig, client.Options{Scheme: hypershiftscheme.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	csrManager := csrminting.NewDefaultManager(kubeClient, ctrlClient)

	privateKey, err := certs.GeneratePrivateKey(defaultRSAKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	subject := certs.BuildDiagnosticsSubject()
	csrPEM, err := certs.GenerateCSR(privateKey, subject)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSR: %w", err)
	}

	csrName, err := csrManager.CreateCSR(ctx, csrPEM, clusterID, diagnosticsUser, hcpNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = csrManager.CleanupCSR(cleanupCtx, csrName)
		_ = csrManager.CleanupCSRApproval(cleanupCtx, csrName, hcpNamespace)
	}

	err = csrManager.CreateCSRApproval(ctx, csrName, hcpNamespace, clusterID, diagnosticsUser)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to create CSR approval: %w", err)
	}

	err = csrManager.WaitForCSRApproval(ctx, csrName, csrTimeout)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed waiting for CSR approval: %w", err)
	}

	clientCert, err := csrManager.WaitForCertificate(ctx, csrName, csrTimeout)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed waiting for certificate: %w", err)
	}

	caCert, err := getKASCACertificate(ctx, kubeClient, hcpNamespace)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to get KAS CA certificate: %w", err)
	}

	kubeconfigYAML, err := buildKubeconfigYAML(privateKey, clientCert, caCert, kasEndpoint, clusterID)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	return &KubeconfigResult{
		KubeconfigYAML: kubeconfigYAML,
		Cleanup:        cleanup,
	}, nil
}

func getKASCACertificate(ctx context.Context, kubeClient kubernetes.Interface, namespace string) ([]byte, error) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, rootCASecret, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s secret: %w", rootCASecret, err)
	}

	caCertData, exists := secret.Data["ca.crt"]
	if !exists {
		return nil, fmt.Errorf("ca.crt not found in %s secret", rootCASecret)
	}

	return caCertData, nil
}

func buildKubeconfigYAML(privateKey *rsa.PrivateKey, clientCert, caCert []byte, server, clusterID string) ([]byte, error) {
	config := clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterID: {
				Server:                   server,
				CertificateAuthorityData: caCert,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			diagnosticsUser: {
				ClientCertificateData: clientCert,
				ClientKeyData:         certs.EncodePrivateKey(privateKey),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			clusterID: {
				Cluster:  clusterID,
				AuthInfo: diagnosticsUser,
			},
		},
		CurrentContext: clusterID,
	}

	return clientcmd.Write(config)
}
