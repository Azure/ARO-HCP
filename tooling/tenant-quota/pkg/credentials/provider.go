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

package credentials

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const (
	secretsStorePath   = "/mnt/secrets-store"
	secretsStoreEnvVar = "SECRETS_STORE_PATH"

	defaultWatchInterval = time.Minute
)

var clientIDPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type credentialKey struct {
	tenantID         string
	clientIDName     string
	clientSecretName string
}

type Provider struct {
	logger *slog.Logger
	root   string

	watchInterval time.Duration
	watchStarted  bool
	credCache     map[credentialKey]*azidentity.ClientSecretCredential
	credMu        sync.Mutex
}

func NewProvider(logger *slog.Logger) *Provider {
	root := os.Getenv(secretsStoreEnvVar)
	if root == "" {
		root = secretsStorePath
	}
	return &Provider{logger: logger, root: root, watchInterval: defaultWatchInterval, credCache: make(map[credentialKey]*azidentity.ClientSecretCredential)}
}

func (p *Provider) ValidateCredentials(tenants []config.TenantConfig) error {
	for _, tenant := range tenants {
		if _, err := p.GetCredential(tenant); err != nil {
			return fmt.Errorf("tenant %s: %w", tenant.GetDisplayName(), err)
		}
		p.logger.Info("Loaded startup credentials; rotation requires restart", "tenant", tenant.GetDisplayName())
	}
	return nil
}

func (p *Provider) GetCredential(tenant config.TenantConfig) (*azidentity.ClientSecretCredential, error) {
	p.credMu.Lock()
	defer p.credMu.Unlock()
	key := credentialKey{tenant.TenantID, tenant.ClientIDKeyVaultSecretName, tenant.ClientSecretKeyVaultSecretName}
	if credential, found := p.credCache[key]; found {
		return credential, nil
	}
	if err := tenant.ValidateCredentialReferences(); err != nil {
		return nil, err
	}
	root := p.root
	if _, err := os.Lstat(filepath.Join(root, "..data")); err == nil {
		root, err = filepath.EvalSymlinks(filepath.Join(root, "..data"))
		if err != nil {
			return nil, fmt.Errorf("resolve credential mount generation")
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect credential mount generation")
	}
	clientID, err := readSecret(root, tenant.ClientIDKeyVaultSecretName)
	if err != nil {
		return nil, fmt.Errorf("client ID: %w", err)
	}
	if !clientIDPattern.MatchString(clientID) {
		return nil, fmt.Errorf("client ID file must contain a UUID")
	}
	clientSecret, err := readSecret(root, tenant.ClientSecretKeyVaultSecretName)
	if err != nil {
		return nil, fmt.Errorf("client secret: %w", err)
	}
	credential, err := azidentity.NewClientSecretCredential(tenant.TenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("create credential: invalid tenant or credential configuration")
	}
	p.credCache[key] = credential
	return credential, nil
}

func readSecret(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return "", fmt.Errorf("cannot read credential file")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("credential file is empty")
	}
	return value, nil
}

// StartWatching watches every mounted client-ID and client-secret file and
// calls onChange once if any of them changes. Credentials are cached for the
// life of the process, so onChange is expected to shut the process down and let
// Kubernetes restart the container, which loads the new files.
//
// Callers must start the watchers before loading credentials: a rotation that
// lands between the load and the watcher baseline would otherwise go unnoticed
// and leave the process serving stale credentials.
func (p *Provider) StartWatching(ctx context.Context, tenants []config.TenantConfig, onChange func()) error {
	if p.watchStarted {
		return nil
	}

	type target struct {
		path   string
		field  string
		tenant string
	}
	targets := map[string]target{}
	for _, tenant := range tenants {
		for field, name := range map[string]string{
			"client ID":     tenant.ClientIDKeyVaultSecretName,
			"client secret": tenant.ClientSecretKeyVaultSecretName,
		} {
			path := filepath.Join(p.root, name)
			if _, err := readSecret(p.root, name); err != nil {
				return fmt.Errorf("tenant %s: %s: %w", tenant.GetDisplayName(), field, err)
			}
			targets[path] = target{path: path, field: field, tenant: tenant.GetDisplayName()}
		}
	}

	var once sync.Once
	watchCtx := utils.ContextWithLogger(ctx, utils.DefaultLogger())
	for _, watch := range targets {
		watcher, err := utils.NewFSWatcher(watch.path, p.watchInterval, func(context.Context) error {
			once.Do(func() {
				p.logger.Warn("Mounted credential file changed; shutting down to reload credentials",
					"tenant", watch.tenant, "credential", watch.field)
				onChange()
			})
			return nil
		})
		if err != nil {
			return fmt.Errorf("create credential watcher for tenant %s", watch.tenant)
		}
		if err := watcher.Start(watchCtx); err != nil {
			return fmt.Errorf("start credential watcher for tenant %s: %s is not readable", watch.tenant, watch.field)
		}
	}
	p.watchStarted = true
	return nil
}
