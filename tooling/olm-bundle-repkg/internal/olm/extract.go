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

package olm

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"

	containerregistrypkgv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"pkg.package-operator.run/cardboard/kubeutils/kubemanifests"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/rukpak/convert"
)

const convertedManifestFile = "manifests/manifest.yaml"

// ExtractOLMBundleImage takes an OLM registry v1 bundle OCI,
// converts it into static manifests and returns a list all objects contained.
func ExtractOLMBundleImage(_ context.Context, image containerregistrypkgv1.Image) (
	objects []unstructured.Unstructured, reg convert.RegistryV1, err error,
) {
	rawFS := fstest.MapFS{}
	reader := mutate.Extract(image)

	defer func() {
		if cErr := reader.Close(); err == nil && cErr != nil {
			err = cErr
		}
	}()
	tarReader := tar.NewReader(reader)

	for {
		hdr, err := tarReader.Next()
		if err != nil && errors.Is(err, io.EOF) {
			break
		}

		path := hdr.Name
		if strings.HasPrefix(path, "../") {
			continue
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, reg, fmt.Errorf("read file header from layer: %w", err)
		}

		rawFS[path] = &fstest.MapFile{
			Data: data,
		}
	}

	if len(rawFS) == 0 {
		return nil, reg, fmt.Errorf("package image contains no files. Might be corrupted")
	}

	convertedFS, reg, err := convert.RegistryV1ToPlain(rawFS, "", nil)
	if err != nil {
		return nil, reg, fmt.Errorf("converting OLM Bundle to static manifests: %w", err)
	}
	manifestBytes, err := fs.ReadFile(convertedFS, convertedManifestFile)
	if err != nil {
		return nil, reg, fmt.Errorf("reading converted manifests: %w", err)
	}
	objects, err = kubemanifests.LoadKubernetesObjectsFromBytes(manifestBytes)
	if err != nil {
		return nil, reg, fmt.Errorf("loading objects from manifests: %w", err)
	}
	return objects, reg, nil
}

// ExtractOLMManifestsDirectory loads manifests directly from a directory containing
// pre-extracted OLM bundle manifests and returns the objects and registry metadata.
func ExtractOLMManifestsDirectory(_ context.Context, manifestsDir string) (
	objects []unstructured.Unstructured, reg convert.RegistryV1, err error,
) {
	// Check if directory exists
	if _, err := os.Stat(manifestsDir); os.IsNotExist(err) {
		return nil, reg, fmt.Errorf("manifests directory does not exist: %s", manifestsDir)
	}

	// Read all YAML files from the directory
	err = filepath.WalkDir(manifestsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-YAML files
		if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		// Read file content
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %v", path, err)
		}

		// Parse YAML documents (may contain multiple documents)
		yamlObjects, err := kubemanifests.LoadKubernetesObjectsFromBytes(data)
		if err != nil {
			return fmt.Errorf("failed to parse YAML from file %s: %v", path, err)
		}

		// Process each object
		for _, obj := range yamlObjects {

			// Categorize objects for RegistryV1
			switch obj.GetKind() {
			case "ClusterServiceVersion":
				csv := operatorsv1alpha1.ClusterServiceVersion{}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &csv); err != nil {
					return fmt.Errorf("failed to convert ClusterServiceVersion: %v", err)
				}
				reg.CSV = csv
				// Extract package name from CSV name (remove version suffix)
				csvName := csv.GetName()
				if idx := strings.LastIndex(csvName, ".v"); idx > 0 {
					reg.PackageName = csvName[:idx]
				} else {
					reg.PackageName = csvName
				}

			case "CustomResourceDefinition":
				crd := apiextensionsv1.CustomResourceDefinition{}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd); err != nil {
					return fmt.Errorf("failed to convert CustomResourceDefinition: %v", err)
				}
				reg.CRDs = append(reg.CRDs, crd)

			default:
				reg.Others = append(reg.Others, obj)
			}
		}

		return nil
	})

	if err != nil {
		return nil, reg, fmt.Errorf("failed to read manifests directory: %v", err)
	}

	// Verify we found a ClusterServiceVersion
	if reg.CSV.GetName() == "" {
		return nil, reg, fmt.Errorf("no ClusterServiceVersion found in manifests directory")
	}

	// We need to extract the operator installation objects from the CSV
	// Setting the targetNamspaces to []string{""} forces the install mode to be InstallModeTypeAllNamespaces
	// This won't work unless the CSV explicitly supports it
	plain, err := convert.Convert(reg, "", nil)
	if err != nil {
		return nil, reg, fmt.Errorf("failed to convert CSV to static manifests: %w", err)
	}

	// Convert the plain.Objects (client.Object) to unstructured.Unstructured
	// Some objects may already be unstructured, others need conversion
	for _, obj := range plain.Objects {
		var unstruct unstructured.Unstructured

		// Check if the object is already unstructured
		if u, ok := obj.(*unstructured.Unstructured); ok {
			unstruct = *u
		} else {
			// Convert structured object to unstructured
			objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return nil, reg, fmt.Errorf("failed to convert object to unstructured: %v", err)
			}
			unstruct = unstructured.Unstructured{Object: objMap}
		}

		objects = append(objects, unstruct)
	}

	if len(objects) == 0 {
		return nil, reg, fmt.Errorf("no manifests found in directory %s", manifestsDir)
	}

	return objects, reg, nil
}

const (
	olmManifestFolder = "manifests"
	olmMetadataFolder = "metadata"
)

const (
	// OCIPathPrefix defines under which subfolder files within a package container should be located.
	OCIPathPrefix = "package"
	// Package manifest filename without file-extension.
	PackageManifestFilename = "manifest"
	// Package manifest lock filename without file-extension.
	PackageManifestLockFilename = "manifest.lock"
	// Name of the components folder for multi-components.
	ComponentsFolder = "components"
)

// Checks image contents to see if it is an OLM bundle image.
func IsOLMBundleImage(image containerregistrypkgv1.Image) (isOLM bool, err error) {
	var (
		packageManifestFound bool
		manifestsFolderFound bool
		metadataFolderFound  bool
	)

	reader := mutate.Extract(image)
	defer func() {
		if cErr := reader.Close(); err == nil && cErr != nil {
			err = cErr
		}
	}()
	tarReader := tar.NewReader(reader)

	for {
		hdr, err := tarReader.Next()
		if err != nil && errors.Is(err, io.EOF) {
			break
		}

		pkgManifestPath := filepath.Join(OCIPathPrefix, PackageManifestFilename)
		switch hdr.Name {
		case pkgManifestPath + ".yml", pkgManifestPath + ".yaml":
			packageManifestFound = true
		}
		if strings.HasPrefix(hdr.Name, olmManifestFolder+"/") {
			manifestsFolderFound = true
		}
		if strings.HasPrefix(hdr.Name, olmMetadataFolder+"/") {
			metadataFolderFound = true
		}
	}
	return !packageManifestFound && manifestsFolderFound && metadataFolderFound, nil
}
