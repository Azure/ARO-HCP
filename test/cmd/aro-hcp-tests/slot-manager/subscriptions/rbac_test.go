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

package subscriptions

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The Go parameter names are a contract with the Bicep modules. Fail when either side drifts.
func TestDeploymentParametersMatchCompiledTemplates(t *testing.T) {
	t.Parallel()
	bot, err := CIBotRBAC("bot", CIBotOptions{GrantAKSRBAC: true})
	if err != nil {
		t.Fatal(err)
	}
	mock, err := MockRBAC(MockPrincipals{FirstParty: "a", ARMHelper: "b", MSIMock: "c", ClustersServiceARMHelper: "d"})
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range []Deployment{bot, mock} {
		declared, _ := deployment.Template["parameters"].(map[string]any)
		for name := range deployment.Parameters {
			if _, found := declared[name]; !found {
				t.Errorf("%s passes parameter %q that the compiled template does not declare", deployment.Name, name)
			}
		}
		for name, definition := range declared {
			_, hasDefault := definition.(map[string]any)["defaultValue"]
			if _, passed := deployment.Parameters[name]; !passed && !hasDefault {
				t.Errorf("%s does not pass required template parameter %q", deployment.Name, name)
			}
		}
	}
}

func TestMockRBACOmitsUnsetClustersServiceHelper(t *testing.T) {
	t.Parallel()
	deployment, err := MockRBAC(MockPrincipals{FirstParty: "a", ARMHelper: "b", MSIMock: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := deployment.Parameters["clustersServiceArmHelperPrincipalId"]; found {
		t.Fatal("an unset optional principal must keep the template default")
	}
}

type fakeDeployments struct {
	deployed []string
	fail     map[string]bool
}

func (f *fakeDeployments) Deploy(_ context.Context, deployment Deployment) error {
	f.deployed = append(f.deployed, deployment.Name)
	if f.fail[deployment.Name] {
		return errors.New("conflict")
	}
	return nil
}

func TestDeployContinuesAcrossSubscriptionsAndReportsAllFailures(t *testing.T) {
	t.Parallel()
	clients := map[string]*fakeDeployments{"one": {fail: map[string]bool{"first": true}}, "two": {}}
	deployer := &Deployer{NewClient: func(id string) (DeploymentClient, error) {
		if client, found := clients[id]; found {
			return client, nil
		}
		return nil, errors.New("no access")
	}}
	err := deployer.Deploy(context.Background(), []string{"one", "absent", "two"}, Deployment{Name: "first"}, Deployment{Name: "second"})
	if err == nil || !strings.Contains(err.Error(), "subscription one: deployment first: conflict") || !strings.Contains(err.Error(), "subscription absent: create deployment client") {
		t.Fatalf("expected both failures, got %v", err)
	}
	if len(clients["two"].deployed) != 2 || len(clients["one"].deployed) != 2 {
		t.Fatalf("remaining deployments must still run: %v %v", clients["one"].deployed, clients["two"].deployed)
	}
}
