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

package framework

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const hyperShiftV2ConditionsName = "should have all expected conditions with correct status"

// HyperShiftV2Config supplies the executable, management access, and artifact
// destination for verifying a cluster owned by the ARO-HCP test scenario.
type HyperShiftV2Config struct {
	Binary               string
	ManagementKubeconfig string
	ArtifactDir          string
}

// Validate checks local prerequisites before the scenario provisions resources.
func (c HyperShiftV2Config) Validate() error {
	info, err := os.Stat(c.Binary)
	if err != nil {
		return fmt.Errorf("HyperShift v2 executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("HyperShift v2 binary %q is not executable", c.Binary)
	}
	if c.ManagementKubeconfig == "" {
		return errors.New("HYPERSHIFT_V2_MANAGEMENT_KUBECONFIG must identify a management-cluster kubeconfig")
	}
	if _, err := clientcmd.BuildConfigFromFlags("", c.ManagementKubeconfig); err != nil {
		return fmt.Errorf("load HyperShift v2 management kubeconfig: %w", err)
	}
	if c.ArtifactDir == "" {
		return errors.New("HyperShift v2 artifact directory is required")
	}
	return nil
}

// RunHyperShiftV2Conditions runs the existing upstream conditions check against
// the unique HostedCluster annotated with resourceID. It preserves native JUnit
// and process output, and fails when the expected check did not execute or pass.
// The caller retains ownership of cluster readiness, diagnostics, and deletion.
func RunHyperShiftV2Conditions(ctx context.Context, config HyperShiftV2Config, resourceID string, output io.Writer) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if _, err := azcorearm.ParseResourceID(resourceID); err != nil {
		return fmt.Errorf("invalid ARO-HCP resource ID: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	managementConfig, err := clientcmd.BuildConfigFromFlags("", config.ManagementKubeconfig)
	if err != nil {
		return fmt.Errorf("load management kubeconfig: %w", err)
	}
	managementConfig.Timeout = 30 * time.Second
	managementClient, err := dynamic.NewForConfig(managementConfig)
	if err != nil {
		return fmt.Errorf("create management client: %w", err)
	}
	target, err := resolveHyperShiftV2Target(ctx, managementClient, resourceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.ArtifactDir, 0755); err != nil {
		return fmt.Errorf("create v2 artifact directory: %w", err)
	}
	// A new directory prevents a stale report from satisfying this invocation.
	dir, err := os.MkdirTemp(config.ArtifactDir, "conditions-")
	if err != nil {
		return fmt.Errorf("create v2 invocation directory: %w", err)
	}
	metadata, err := json.MarshalIndent(struct {
		ResourceID string               `json:"resourceID"`
		Target     types.NamespacedName `json:"target"`
		Label      string               `json:"label"`
	}{resourceID, target, "hosted-cluster-conditions"}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode v2 target metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.json"), metadata, 0644); err != nil {
		return fmt.Errorf("write v2 target metadata: %w", err)
	}
	logFile, err := os.Create(filepath.Join(dir, "output.log"))
	if err != nil {
		return fmt.Errorf("create v2 output log: %w", err)
	}
	defer logFile.Close()
	junitPath := filepath.Join(dir, "junit_hypershift_v2.xml")
	cmd := exec.CommandContext(ctx, config.Binary,
		"-test.run=^TestE2EV2$",
		"--ginkgo.label-filter=hosted-cluster-conditions",
		"--ginkgo.fail-on-empty",
		"--ginkgo.no-color",
		"--ginkgo.timeout=15m",
		"--e2e.junit-report="+junitPath,
	)
	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+config.ManagementKubeconfig,
		"KUBECONFIG_SOURCE=", "KUBECONFIG_TARGET=", "E2E_SHOW_ENV_HELP=",
		"E2E_HOSTED_CLUSTER_NAME="+target.Name,
		"E2E_HOSTED_CLUSTER_NAMESPACE="+target.Namespace,
		"ARTIFACT_DIR="+dir,
	)
	cmd.Stdout = io.MultiWriter(output, logFile)
	cmd.Stderr = cmd.Stdout
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	if runErr != nil {
		runErr = fmt.Errorf("run HyperShift v2 conditions for %s: %w", target, errors.Join(runErr, ctx.Err()))
	}
	return errors.Join(runErr, validateHyperShiftV2JUnit(junitPath))
}

func resolveHyperShiftV2Target(ctx context.Context, client dynamic.Interface, resourceID string) (types.NamespacedName, error) {
	clusters, err := client.Resource(schema.GroupVersionResource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters",
	}).List(ctx, metav1.ListOptions{})
	if err != nil {
		return types.NamespacedName{}, fmt.Errorf("list management HostedClusters for %q: %w", resourceID, err)
	}
	var matches []types.NamespacedName
	for _, cluster := range clusters.Items {
		if strings.EqualFold(cluster.GetAnnotations()[hypershiftv1beta1.ManagedAzureResourceIDAnnotation], resourceID) {
			matches = append(matches, types.NamespacedName{Name: cluster.GetName(), Namespace: cluster.GetNamespace()})
		}
	}
	if len(matches) != 1 {
		return types.NamespacedName{}, fmt.Errorf("expected one HostedCluster annotated with Azure resource ID %q, found %d", resourceID, len(matches))
	}
	return matches[0], nil
}

func validateHyperShiftV2JUnit(path string) error {
	report, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open HyperShift v2 JUnit report: %w", err)
	}
	defer report.Close()
	decoder := xml.NewDecoder(report)
	count := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode HyperShift v2 JUnit report: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "testcase" {
			continue
		}
		var testcase struct {
			Name    string    `xml:"name,attr"`
			Skipped *struct{} `xml:"skipped"`
			Failure *struct{} `xml:"failure"`
			Error   *struct{} `xml:"error"`
		}
		if err := decoder.DecodeElement(&testcase, &start); err != nil {
			return fmt.Errorf("decode HyperShift v2 testcase: %w", err)
		}
		count++
		if !strings.HasSuffix(testcase.Name, hyperShiftV2ConditionsName) {
			return fmt.Errorf("unexpected HyperShift v2 testcase %q", testcase.Name)
		}
		if testcase.Skipped != nil || testcase.Failure != nil || testcase.Error != nil {
			return fmt.Errorf("HyperShift v2 conditions testcase %q did not pass; see %s", testcase.Name, path)
		}
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one executed HyperShift v2 conditions testcase, found %d in %s", count, path)
	}
	return nil
}
