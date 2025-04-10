package olm

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing/fstest"

	containerregistrypkgv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/cardboard/kubeutils/kubemanifests"

	"github.com/Azure/ARO-HCP/tooling/mcerepkg/internal/rukpak/convert"
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
