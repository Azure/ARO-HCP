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

package framework

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/config/types"

	"github.com/Azure/ARO-HCP/test/util/config"
)

type mustGatherCollector struct {
	getenv         func(string) string
	lookPath       func(string) (string, error)
	serviceConfig  func() (types.Configuration, error)
	subscriptionID func(context.Context) (string, error)
	run            func(context.Context, string, ...string) error
	logger         logr.Logger
}

func (collector mustGatherCollector) collect(ctx context.Context, failed bool, resourceGroups []string, logDir string) {
	if !failed {
		collector.logger.Info("skipping hcpctl must-gather: test passed (or did not fail)")
		return
	}
	if len(resourceGroups) == 0 {
		collector.logger.Info("skipping hcpctl must-gather: no resource group created by test")
		return
	}
	outputRoot := collector.getenv("ARTIFACT_DIR")
	if outputRoot == "" {
		if logDir == "" {
			collector.logger.Info("skipping hcpctl must-gather: ARTIFACT_DIR unset and test LogDirPath unavailable")
			return
		}
		collector.logger.Info("ARTIFACT_DIR unset: using test LogDirPath for hcpctl must-gather", "outputPath", logDir)
		outputRoot = logDir
	}
	binary := collector.getenv("HCPCTL_BINARY")
	if binary == "" {
		var err error
		binary, err = collector.lookPath("hcpctl")
		if err != nil {
			collector.logger.Info("skipping hcpctl must-gather: HCPCTL_BINARY unset and hcpctl not in PATH")
			return
		}
	}
	serviceConfig, err := collector.serviceConfig()
	if err != nil {
		collector.logger.Info("skipping hcpctl must-gather: kusto config missing (service config unavailable)")
		return
	}
	kustoCluster, err := config.GetStringByPath(serviceConfig, "kusto.kustoName")
	if err != nil {
		collector.logger.Info("skipping hcpctl must-gather: kusto config missing (kusto.kustoName)")
		return
	}
	kustoRegion, err := config.GetStringByPath(serviceConfig, "kusto.location")
	if err != nil {
		collector.logger.Info("skipping hcpctl must-gather: kusto config missing (kusto.location)")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	subscriptionID, err := collector.subscriptionID(ctx)
	if err != nil || subscriptionID == "" {
		collector.logger.Info("skipping hcpctl must-gather: subscription ID unavailable")
		return
	}
	for _, resourceGroup := range resourceGroups {
		if err := ctx.Err(); err != nil {
			collector.logger.Info("skipping hcpctl must-gather: collection context expired", "resourceGroup", resourceGroup)
			continue
		}
		outputPath, err := os.MkdirTemp("", "hcpctl-must-gather-")
		if err != nil {
			collector.logger.Error(err, "creating must-gather temporary directory failed; continuing cleanup", "resourceGroup", resourceGroup)
			continue
		}
		defer func() {
			if err := os.RemoveAll(outputPath); err != nil {
				collector.logger.Error(err, "removing must-gather temporary directory failed; continuing cleanup", "outputPath", outputPath)
			}
		}()
		collector.logger.Info("running hcpctl must-gather before resource deletion", "resourceGroup", resourceGroup, "outputPath", outputPath)
		args := []string{
			"must-gather", "query",
			"--subscription-id", subscriptionID,
			"--resource-group", resourceGroup,
			"--kusto", kustoCluster,
			"--region", kustoRegion,
			"--output-path", outputPath,
		}
		if err := collector.run(ctx, binary, args...); err != nil {
			collector.logger.Error(err, "hcpctl must-gather failed; continuing cleanup", "resourceGroup", resourceGroup)
		}
		entries, err := os.ReadDir(outputPath)
		if err != nil {
			collector.logger.Error(err, "reading must-gather output failed; continuing cleanup", "resourceGroup", resourceGroup)
			continue
		}
		if len(entries) == 0 {
			collector.logger.Info("no must-gather content produced; nothing to archive", "resourceGroup", resourceGroup)
			continue
		}
		archivePath := filepath.Join(outputRoot, "must-gather", resourceGroup+".zip")
		const archiveTimeout = 2 * time.Minute
		archiveCtx, cancelArchive := context.WithTimeout(context.Background(), archiveTimeout)
		err = archiveMustGather(archiveCtx, outputPath, archivePath)
		cancelArchive()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				collector.logger.Error(err, "must-gather archiving canceled (budget exceeded); removed partial archive", "resourceGroup", resourceGroup)
				continue
			}
			collector.logger.Error(err, "archiving must-gather output failed; continuing cleanup", "resourceGroup", resourceGroup)
			continue
		}
		collector.logger.Info("created must-gather archive", "resourceGroup", resourceGroup, "outputPath", archivePath)
	}
}

func archiveMustGather(ctx context.Context, sourceDir, archivePath string) (err error) {
	if err := os.MkdirAll(filepath.Dir(archivePath), 0755); err != nil {
		return err
	}
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(mustGatherContextWriter{ctx: ctx, writer: archiveFile})
	defer func() {
		err = errors.Join(err, writer.Close(), archiveFile.Close(), ctx.Err())
		if err != nil {
			err = errors.Join(err, os.Remove(archivePath))
		}
	}()
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported must-gather file type: %s", path)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		destination, err := writer.CreateHeader(header)
		if err != nil || info.IsDir() {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(destination, mustGatherContextReader{ctx: ctx, reader: source})
		return errors.Join(copyErr, source.Close())
	})
}

type mustGatherContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader mustGatherContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

type mustGatherContextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer mustGatherContextWriter) Write(buffer []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	return writer.writer.Write(buffer)
}
