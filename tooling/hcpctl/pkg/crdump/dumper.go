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

package crdump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// - "outputPath": base directory for output files
// - "hostedClusterNamespace": namespace being dumped
type CROutputOptions struct {
	OutputPath             string
	HostedClusterNamespace string
}

// CROutputFunc defines how custom resources should be processed and output.
// This function receives CR data through crChan and should process it according
// to the configuration in options.
type CROutputFunc func(crChan <-chan *unstructured.UnstructuredList, options CROutputOptions) error

type Dumper struct {
	lister        CustomResourceLister
	outputFunc    CROutputFunc
	outputOptions CROutputOptions
}

// NewDumper creates a new Dumper with custom output function and options.
// This constructor provides full control over how CR data is processed and output.
func NewDumper(lister CustomResourceLister, outputFunc CROutputFunc, outputOptions CROutputOptions) *Dumper {
	return &Dumper{
		lister:        lister,
		outputFunc:    outputFunc,
		outputOptions: outputOptions,
	}
}

// NewCliDumper creates a new Dumper with file-based output for CLI usage.
func NewCliDumper(lister CustomResourceLister, outputPath, hostedClusterNamespace string) *Dumper {
	outputOptions := CROutputOptions{
		OutputPath:             outputPath,
		HostedClusterNamespace: hostedClusterNamespace,
	}

	return &Dumper{
		lister:        lister,
		outputFunc:    cliOutputFunc,
		outputOptions: outputOptions,
	}
}

func cliOutputFunc(crChan <-chan *unstructured.UnstructuredList, options CROutputOptions) error {
	outputPath := options.OutputPath
	hostedClusterNamespace := options.HostedClusterNamespace

	outputDir := filepath.Join(outputPath, "crs", hostedClusterNamespace)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", outputDir, err)
	}

	openedFiles := make(map[string]*os.File)
	var allErrors error

	defer func() {
		for _, file := range openedFiles {
			if closeErr := file.Close(); closeErr != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to close file: %w", closeErr))
			}
		}
	}()

	for crList := range crChan {
		if len(crList.Items) == 0 {
			continue
		}

		gvk := crList.GroupVersionKind()
		filename := filepath.Join(outputDir, strings.ToLower(gvk.Kind)+"."+gvk.Group+".yaml")

		file, ok := openedFiles[filename]
		if !ok {
			newFile, err := os.Create(filename)
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file %s: %w", filename, err))
				continue
			}
			openedFiles[filename] = newFile
			file = newFile
		}

		for i, item := range crList.Items {
			if i > 0 {
				if _, err := file.WriteString("---\n"); err != nil {
					allErrors = errors.Join(allErrors, fmt.Errorf("failed to write separator: %w", err))
					continue
				}
			}
			data, err := yaml.Marshal(item.Object)
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to marshal %s/%s: %w", item.GetNamespace(), item.GetName(), err))
				continue
			}
			if _, err := file.Write(data); err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to write to file: %w", err))
			}
		}
	}

	return allErrors
}

// DumpCRs lists all custom resources for the given namespace and streams them through the output function.
func (d *Dumper) DumpCRs(ctx context.Context, hostedClusterNamespace string) error {
	crChan := make(chan *unstructured.UnstructuredList)

	outputFnGroup := new(errgroup.Group)

	// Call output fn in a separate goroutine.
	outputFnGroup.Go(func() error {
		return d.outputFunc(crChan, d.outputOptions)
	})

	crdList, err := d.lister.ListCRDs(ctx)
	if err != nil {
		return err
	}

	// List CRs and send to channel
	listErr := d.lister.StreamCRs(ctx, hostedClusterNamespace, crdList, crChan)

	// Close channel to signal completion to output fn
	close(crChan)

	// Wait for output fn to complete
	outputErr := outputFnGroup.Wait()

	if listErr != nil {
		return fmt.Errorf("failed to list CRs: %w", listErr)
	}
	if outputErr != nil {
		return fmt.Errorf("failed to output CRs: %w", outputErr)
	}

	return nil
}
