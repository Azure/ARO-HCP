// Copyright 2026 Microsoft Corporation
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

// Package configoverride builds the CI configuration overlay without publishing
// partially validated inputs or interpreting environment values as expressions.
package configoverride

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

type Inputs struct {
	DeployEnvironment            string
	Images                       map[string]string
	OCLoginRegistries            string
	MSIMockLease                 string
	ARMHelperLeases              []string
	MSIContainers                string
	InfrastructureIdentityBundle string
}

type Paths struct {
	MSIMockCatalog    string
	ARMHelperCatalog  string
	HypershiftOverlay string
}

type Result struct {
	YAML              []byte
	OCLoginRegistries string
}

var imageComponents = [...]struct{ environment, component string }{
	{"BACKEND_IMAGE", "backend"},
	{"FRONTEND_IMAGE", "frontend"},
	{"ADMIN_API_IMAGE", "adminApi"},
	{"SESSIONGATE_IMAGE", "sessiongate"},
	{"HCP_RECOVERY_IMAGE", "hcpRecovery"},
	{"FLEET_IMAGE", "fleet"},
	{"MGMT_AGENT_IMAGE", "mgmtAgent"},
	{"SWIFT_RECORDER_IMAGE", "swiftRecorder"},
	{"KUBE_APPLIER_IMAGE", "kubeApplier"},
	{"EXPORTER_IMAGE", "customExporter"},
}

var (
	resourceGroupName = regexp.MustCompile(`^[A-Za-z0-9._()-]*[A-Za-z0-9_()-]$`)
	identityName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	stampName         = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// InputsFromEnvironment reads CI overlay inputs and rejects unknown *_IMAGE
// variables, including empty ones, so misspelled overrides cannot go unnoticed.
func InputsFromEnvironment(environ []string) (Inputs, error) {
	input := Inputs{Images: make(map[string]string)}
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case leaseenv.DeployEnvironment:
			input.DeployEnvironment = value
		case leaseenv.MSIMockSP:
			input.MSIMockLease = value
		case leaseenv.ARMHelperSP:
			input.ARMHelperLeases = strings.Fields(value)
		case leaseenv.MSIContainers:
			input.MSIContainers = value
		case leaseenv.InfrastructureIdentityBundle:
			input.InfrastructureIdentityBundle = value
		case "USE_OC_LOGIN_REGISTRIES":
			input.OCLoginRegistries = value
		default:
			if strings.HasSuffix(name, "_IMAGE") {
				input.Images[name] = value
			}
		}
	}
	if err := validateImageKeys(input.Images); err != nil {
		return Inputs{}, err
	}
	return input, nil
}

func validateImageKeys(images map[string]string) error {
	var unknown []string
	for name := range images {
		known := false
		for _, image := range imageComponents {
			if name == image.environment {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	known := make([]string, len(imageComponents))
	for i, image := range imageComponents {
		known[i] = image.environment
	}
	return fmt.Errorf("unknown image override keys: %s; remove or rename them to supported keys: %s", strings.Join(unknown, ", "), strings.Join(known, ", "))
}

// applyImageOverrides keeps CI image selection and source-registry authentication
// together. USE_OC_LOGIN_REGISTRIES tells the ARO-Tools mirroring step to use
// oc registry login for these sources before copying the CI-built images to ACR.
// Preserve any existing entries and append the registry of each overridden image.
func applyImageOverrides(defaults map[string]interface{}, images map[string]string, registries string) (string, error) {
	for _, image := range imageComponents {
		ref := images[image.environment]
		if ref == "" {
			continue
		}
		name, digest, hasDigest := strings.Cut(ref, "@")
		registry, repository, hasRepository := strings.Cut(name, "/")
		if !hasDigest || !hasRepository || registry == "" || repository == "" || digest == "" || strings.Contains(digest, "@") {
			return "", fmt.Errorf("%s must be a digest-based ref (registry/repo@sha256:...), got %q", image.environment, ref)
		}
		defaults[image.component] = map[string]interface{}{"image": map[string]interface{}{
			"registry": registry, "repository": repository, "digest": digest,
		}}
		if registries != "" {
			registries += " "
		}
		registries += registry
	}
	return registries, nil
}

// Generate validates inputs and reads only the catalogs needed for selected leases.
// It does not write files or modify the process environment.
func Generate(input Inputs, paths Paths) (Result, error) {
	if err := validateImageKeys(input.Images); err != nil {
		return Result{}, err
	}
	if input.DeployEnvironment == "" {
		return Result{}, errors.New("deployment environment must not be empty")
	}
	if len(input.ARMHelperLeases) > 2 {
		return Result{}, fmt.Errorf("%s must contain at most two lease names, got %d", leaseenv.ARMHelperSP, len(input.ARMHelperLeases))
	}

	defaults := map[string]interface{}{}
	registries, err := applyImageOverrides(defaults, input.Images, input.OCLoginRegistries)
	if err != nil {
		return Result{}, err
	}

	if input.MSIMockLease != "" {
		entries, err := readPool(paths.MSIMockCatalog, "miMockPool")
		if err != nil {
			return Result{}, err
		}
		principal, err := selectPrincipal(entries, input.MSIMockLease, paths.MSIMockCatalog)
		if err != nil {
			return Result{}, err
		}
		defaults["miMockClientId"] = principal.clientID
		defaults["miMockPrincipalId"] = principal.principalID
		defaults["miMockCertName"] = principal.certName
	}
	if len(input.ARMHelperLeases) > 0 {
		entries, err := readPool(paths.ARMHelperCatalog, "armHelperPool")
		if err != nil {
			return Result{}, err
		}
		backend, err := selectPrincipal(entries, input.ARMHelperLeases[0], paths.ARMHelperCatalog)
		if err != nil {
			return Result{}, err
		}
		clustersService := backend
		if len(input.ARMHelperLeases) == 2 {
			clustersService, err = selectPrincipal(entries, input.ARMHelperLeases[1], paths.ARMHelperCatalog)
			if err != nil {
				return Result{}, err
			}
		}
		defaults["armHelperClientId"] = backend.clientID
		defaults["armHelperCertName"] = backend.certName
		defaults["clustersServiceArmHelperClientId"] = clustersService.clientID
		defaults["clustersServiceArmHelperCertName"] = clustersService.certName
		// The FPA principal is not the ARM helper's authentication principal.
	}
	if input.InfrastructureIdentityBundle != "" {
		identities, err := infrastructureIdentities(input.InfrastructureIdentityBundle)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", leaseenv.InfrastructureIdentityBundle, err)
		}
		defaults["infrastructureIdentities"] = identities
	}
	if input.MSIContainers == "" {
		// Without leased MSI containers, this is a provision-healthcheck job:
		// one management user node is enough because no E2E workloads run.
		defaults["mgmt"] = map[string]interface{}{"aks": map[string]interface{}{
			"userAgentPool": map[string]interface{}{"minCount": 1},
		}}
	}

	root := map[string]interface{}{}
	if len(defaults) > 0 {
		root["clouds"] = map[string]interface{}{"dev": map[string]interface{}{
			"environments": map[string]interface{}{input.DeployEnvironment: map[string]interface{}{"defaults": defaults}},
		}}
	}
	if paths.HypershiftOverlay != "" {
		data, err := os.ReadFile(paths.HypershiftOverlay)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("read Hypershift overlay %q: %w", paths.HypershiftOverlay, err)
		}
		if err == nil {
			var overlay map[string]interface{}
			if err := decodeYAML(data, &overlay); err != nil {
				return Result{}, fmt.Errorf("parse Hypershift overlay %q: %w", paths.HypershiftOverlay, err)
			}
			mergeMaps(root, overlay)
		}
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return Result{}, fmt.Errorf("encode configuration override: %w", err)
	}
	return Result{YAML: data, OCLoginRegistries: registries}, nil
}

func decodeYAML(data []byte, out interface{}) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("expected exactly one YAML document")
	}
	return nil
}

func readPool(path, pool string) (map[string]yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s catalog %q: %w", pool, path, err)
	}
	var catalog map[string]yaml.Node
	if err := decodeYAML(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse %s catalog %q: %w", pool, path, err)
	}
	node, ok := catalog[pool]
	if !ok || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("catalog %q requires a %s mapping", path, pool)
	}
	var entries map[string]yaml.Node
	if err := node.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse %s in catalog %q: %w", pool, path, err)
	}
	return entries, nil
}

type principal struct {
	clientID, principalID, certName string
}

func selectPrincipal(entries map[string]yaml.Node, lease, path string) (principal, error) {
	node, ok := entries[lease]
	if !ok || node.Kind != yaml.MappingNode {
		return principal{}, fmt.Errorf("lease %q not found or incomplete in catalog %q", lease, path)
	}
	var fields map[string]interface{}
	if err := node.Decode(&fields); err != nil {
		return principal{}, fmt.Errorf("lease %q in catalog %q: %w", lease, path, err)
	}
	result := principal{}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"clientId", &result.clientID},
		{"principalId", &result.principalID},
		{"certName", &result.certName},
	} {
		value, ok := fields[field.name].(string)
		if !ok || value == "" || value == "null" {
			return principal{}, fmt.Errorf("lease %q in catalog %q requires nonempty string %s", lease, path, field.name)
		}
		*field.value = value
	}
	return result, nil
}

func infrastructureIdentities(payload string) (map[string]interface{}, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var bundle leaseenv.InfrastructureIdentityBundlePayload
	for _, field := range []struct {
		name  string
		value interface{}
	}{
		{"serviceResourceGroup", &bundle.ServiceResourceGroup},
		{"serviceAksIdentityName", &bundle.ServiceAksIdentityName},
		{"managementResourceGroups", &bundle.ManagementResourceGroups},
		{"managementAksIdentityNames", &bundle.ManagementAksIdentityNames},
	} {
		value, ok := fields[field.name]
		if !ok {
			return nil, fmt.Errorf("missing required field %s", field.name)
		}
		if err := json.Unmarshal(value, field.value); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", field.name, err)
		}
	}
	if len(fields) != 4 {
		return nil, errors.New("expected exactly serviceResourceGroup, serviceAksIdentityName, managementResourceGroups and managementAksIdentityNames")
	}
	if !validResourceGroup(bundle.ServiceResourceGroup) || !validIdentity(bundle.ServiceAksIdentityName) {
		return nil, errors.New("service resource group must be a valid 1-90 character name and AKS identity a valid 3-128 character name")
	}
	if len(bundle.ManagementResourceGroups) == 0 || len(bundle.ManagementResourceGroups) != len(bundle.ManagementAksIdentityNames) {
		return nil, errors.New("management resource groups and AKS identities must have identical nonempty stamp sets")
	}
	stamps := make([]string, 0, len(bundle.ManagementResourceGroups))
	for stamp, resourceGroup := range bundle.ManagementResourceGroups {
		if !stampName.MatchString(stamp) {
			return nil, fmt.Errorf("invalid management stamp %q: expected a positive decimal integer without leading zero", stamp)
		}
		if !validResourceGroup(resourceGroup) || !validIdentity(bundle.ManagementAksIdentityNames[stamp]) {
			return nil, fmt.Errorf("stamp %q requires a valid resource group and AKS identity name", stamp)
		}
		stamps = append(stamps, stamp)
	}
	// Comparing lengths first orders arbitrarily large positive decimal stamps
	// numerically without overflow or floating-point precision loss.
	sort.Slice(stamps, func(i, j int) bool {
		if len(stamps[i]) != len(stamps[j]) {
			return len(stamps[i]) < len(stamps[j])
		}
		return stamps[i] < stamps[j]
	})
	return map[string]interface{}{
		"useLeased":                  true,
		"serviceResourceGroup":       bundle.ServiceResourceGroup,
		"serviceAksIdentityName":     bundle.ServiceAksIdentityName,
		"managementResourceGroups":   stampCSV(stamps, bundle.ManagementResourceGroups),
		"managementAksIdentityNames": stampCSV(stamps, bundle.ManagementAksIdentityNames),
	}, nil
}

func validResourceGroup(value string) bool {
	return len(value) >= 1 && len(value) <= 90 && resourceGroupName.MatchString(value)
}

func validIdentity(value string) bool {
	return len(value) >= 3 && len(value) <= 128 && identityName.MatchString(value)
}

func stampCSV(stamps []string, values map[string]string) string {
	var result strings.Builder
	for i, stamp := range stamps {
		if i > 0 {
			result.WriteByte(',')
		}
		result.WriteString(stamp)
		result.WriteByte('=')
		result.WriteString(values[stamp])
	}
	return result.String()
}

// mergeMaps recursively combines maps; every other overlay value, including
// arrays and explicit null, replaces the generated value.
func mergeMaps(base, overlay map[string]interface{}) {
	for key, value := range overlay {
		baseMap, baseIsMap := base[key].(map[string]interface{})
		overlayMap, overlayIsMap := value.(map[string]interface{})
		if baseIsMap && overlayIsMap {
			mergeMaps(baseMap, overlayMap)
		} else {
			base[key] = value
		}
	}
}

// WriteOverride atomically replaces path. Its parent directory must already
// exist. A failure leaves an existing file untouched and removes temporary files.
func WriteOverride(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create override temporary file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if n, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write override temporary file: %w", err)
	} else if n != len(data) {
		tmp.Close()
		return fmt.Errorf("write override temporary file: %w", io.ErrShortWrite)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close override temporary file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("set override permissions: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace override %q: %w", path, err)
	}
	return nil
}
