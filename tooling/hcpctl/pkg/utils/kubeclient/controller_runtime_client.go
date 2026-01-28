package kubeclient

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/scale/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AKS Microsoft Entra server application ID
	azureKubernetesServiceAADServerAppID = "6dae42f8-4368-4678-94ff-3960e28e3630"
)

func NewK8sClientFromKubeConfig(
	ctx context.Context,
	kubeconfigBytes []byte,
	credential azcore.TokenCredential,
	schemesToAdd ...func(s *runtime.Scheme) error) (client.Client, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}
	restConfig.AuthProvider = nil

	token, _ := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{azureKubernetesServiceAADServerAppID + "/.default"},
	})
	restConfig.BearerToken = token.Token

	runtimeScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(runtimeScheme); err != nil {
		return nil, err
	}

	for _, addScheme := range schemesToAdd {
		if err := addScheme(runtimeScheme); err != nil {
			return nil, fmt.Errorf("failed to add scheme: %w", err)
		}
	}

	k8sClient, err := client.New(restConfig, client.Options{
		Scheme: runtimeScheme,
	})
	if err != nil {
		return nil, err
	}
	return k8sClient, nil
}
