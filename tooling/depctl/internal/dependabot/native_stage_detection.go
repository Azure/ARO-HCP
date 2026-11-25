// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"golang.org/x/sys/unix"
)

const nativeDetectionOutput = "agent_output.json"
const nativeDetectionPatch = "aw-native-replacement.patch"

// StageNativeDetection expands only the prepared detector copy with digest-verified publication data.
func StageNativeDetection(outputFile, detectionDir string) (map[string]any, error) {
	snapshot, err := nativeReadPublicationSnapshot(outputFile)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return map[string]any{"status": "no-request"}, nil
	}

	directory, err := unix.Open(filepath.Clean(detectionDir), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open detector directory: %w", err)
	}
	defer unix.Close(directory)
	detector, err := nativePublicationOpenFile(directory, nativeDetectionOutput, unix.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("open prepared detector output: %w", err)
	}
	defer detector.Close()
	prepared, info, err := nativePublicationReadFile(detector)
	if err != nil {
		return nil, fmt.Errorf("read prepared detector output: %w", err)
	}
	if info.Dev == snapshot.original.Dev && info.Ino == snapshot.original.Ino {
		return nil, fmt.Errorf("detector output must be a separate copy of original agent output")
	}
	copyEnvelope, err := nativeDecode(prepared)
	if err != nil {
		return nil, fmt.Errorf("decode prepared detector output: %w", err)
	}
	if !reflect.DeepEqual(copyEnvelope, snapshot.envelope) {
		return nil, fmt.Errorf("prepared detector output does not match original agent output")
	}

	request := snapshot.request.request
	title, err := nativeSafeText(request.Title, true)
	if err != nil {
		return nil, fmt.Errorf("stage publication title: %w", err)
	}
	body, err := nativeSafeText(request.Body, false)
	if err != nil {
		return nil, fmt.Errorf("stage publication body: %w", err)
	}
	snapshot.selected["title"] = title
	snapshot.selected["body"] = body
	snapshot.selected["source_prs"] = request.SourcePRs
	snapshot.selected["expected_base_sha"] = request.ExpectedBaseSHA

	var expanded bytes.Buffer
	encoder := json.NewEncoder(&expanded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(snapshot.envelope); err != nil {
		return nil, fmt.Errorf("encode expanded detector output: %w", err)
	}
	contents := bytes.TrimSuffix(expanded.Bytes(), []byte("\n"))
	if err := nativeWriteDetectionPatch(directory, request.Patch); err != nil {
		return nil, err
	}
	if _, err := detector.WriteAt(contents, 0); err != nil {
		return nil, fmt.Errorf("write expanded detector output: %w", err)
	}
	if err := detector.Truncate(int64(len(contents))); err != nil {
		return nil, fmt.Errorf("truncate expanded detector output: %w", err)
	}
	return map[string]any{"status": "staged", "patch": nativeDetectionPatch}, nil
}

// Native detector patches are created exclusively and contain the exact verified patch bytes.
func nativeWriteDetectionPatch(directory int, patch string) error {
	fd, err := unix.Openat(directory, nativeDetectionPatch, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("create native detector patch: %w", err)
	}
	file := os.NewFile(uintptr(fd), nativeDetectionPatch)
	_, writeErr := file.WriteString(patch)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write native detector patch: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close native detector patch: %w", closeErr)
	}
	return nil
}
