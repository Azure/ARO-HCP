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

package minting

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/ARO-HCP/internal/csrminting"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils"
)

type CSRManager = csrminting.CSRManager

type DefaultManager struct {
	inner *csrminting.DefaultManager
}

func NewDefaultManager(restConfig *rest.Config) (*DefaultManager, error) {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ctrlClient, err := client.New(restConfig, client.Options{Scheme: common.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	return &DefaultManager{
		inner: csrminting.NewDefaultManager(kubeClient, ctrlClient),
	}, nil
}

func (mgr *DefaultManager) CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error) {
	sanitizedUser, err := utils.SanitizeUsername(user)
	if err != nil {
		return "", fmt.Errorf("invalid username for CSR naming: %w", err)
	}
	return mgr.inner.CreateCSR(ctx, csrPEM, clusterID, sanitizedUser, namespace)
}

func (mgr *DefaultManager) CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error {
	sanitizedUser, err := utils.SanitizeUsername(user)
	if err != nil {
		return fmt.Errorf("invalid username for kubeconfig authInfo: %w", err)
	}
	return mgr.inner.CreateCSRApproval(ctx, csrName, namespace, clusterID, sanitizedUser)
}

func (mgr *DefaultManager) WaitForCSRApproval(ctx context.Context, name string, timeout time.Duration) error {
	return mgr.inner.WaitForCSRApproval(ctx, name, timeout)
}

func (mgr *DefaultManager) WaitForCertificate(ctx context.Context, name string, timeout time.Duration) ([]byte, error) {
	return mgr.inner.WaitForCertificate(ctx, name, timeout)
}

func (mgr *DefaultManager) CleanupCSR(ctx context.Context, name string) error {
	return mgr.inner.CleanupCSR(ctx, name)
}

func (mgr *DefaultManager) CleanupCSRApproval(ctx context.Context, name, namespace string) error {
	return mgr.inner.CleanupCSRApproval(ctx, name, namespace)
}
