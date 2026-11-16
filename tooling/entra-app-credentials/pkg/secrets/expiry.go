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

package secrets

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/expiry"
)

// matchCredential returns the credential a stored secret was minted from.
// Graph only exposes the first characters of a secret, so credentials are
// matched by that hint. On the rare hint collision the earliest-ending
// credential wins, so a problem is never hidden behind a longer-lived sibling.
func matchCredential(credentials []PasswordCredential, secret string) (PasswordCredential, bool) {
	var match PasswordCredential
	found := false
	for _, credential := range credentials {
		if credential.Hint == "" || !strings.HasPrefix(secret, credential.Hint) {
			continue
		}
		if !found || credential.End.Before(match.End) {
			match, found = credential, true
		}
	}
	return match, found
}

// inspectClientSecret logs the expiry of a stored client secret and returns an
// error when it can no longer authenticate: it expired, or it matches no
// credential of the application. Expiry within the warning window only logs.
func (e *Ensurer) inspectClientSecret(logger logr.Logger, application *Application, name, secret string, warning time.Duration) error {
	credential, found := matchCredential(application.PasswordCredentials, secret)
	if !found {
		return fmt.Errorf("stored client secret %q matches none of the application's %d client secrets; it was deleted or replaced outside Key Vault", name, len(application.PasswordCredentials))
	}

	now := e.now()
	end := credential.End.UTC().Format(time.RFC3339)
	switch expiry.Classify(credential.End, now, warning) {
	case expiry.Expired:
		return fmt.Errorf("client secret %q expired at %s", name, end)
	case expiry.Expiring:
		logger.Info("WARNING: client secret expires soon", "secret", name, "expires", end, "daysRemaining", expiry.Days(credential.End, now))
	default:
		logger.Info("client secret is valid", "secret", name, "expires", end, "daysRemaining", expiry.Days(credential.End, now))
	}
	return nil
}
