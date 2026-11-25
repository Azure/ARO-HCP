// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// Native publication sources bind positive PR identities to immutable commits.
type nativePublicationSource struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Native publication requests contain only locally validated publication inputs.
type nativePublicationRequest struct {
	SourcePRs       []nativePublicationSource `json:"source_prs"`
	ExpectedBaseSHA string                    `json:"expected_base_sha"`
	Patch           string                    `json:"patch"`
	Title           string                    `json:"title"`
	Body            string                    `json:"body"`
}

// NativePublicationRequest is an opaque, digest-verified publication request.
type NativePublicationRequest struct{ request nativePublicationRequest }

// ReadNativePublicationRequest validates the envelope and exact artifact before any authenticated work.
func ReadNativePublicationRequest(outputFile string) (*NativePublicationRequest, error) {
	output, err := nativePublicationReadFile(outputFile, -1)
	if err != nil {
		return nil, fmt.Errorf("read native output: %w", err)
	}
	value, err := nativeDecode(output)
	if err != nil {
		return nil, fmt.Errorf("decode native output: %w", err)
	}
	envelope, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid global agent output")
	}
	items, ok := envelope["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid global agent output items")
	}
	if errors, exists := envelope["errors"]; exists {
		list, ok := errors.([]any)
		if !ok || len(list) != 0 {
			return nil, fmt.Errorf("agent output contains errors")
		}
	}
	var selected map[string]any
	nativeItems := 0
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed output item")
		}
		kind, ok := item["type"].(string)
		if !ok {
			return nil, fmt.Errorf("malformed output item type")
		}
		if kind == "native_replacement" || kind == "native_action" {
			nativeItems++
		}
		if kind == "native_replacement" {
			selected = item
		}
	}
	if nativeItems > 1 {
		return nil, fmt.Errorf("only one native output kind/item may run per workflow")
	}
	if selected == nil {
		return nil, nil
	}
	if len(selected) != 2 {
		return nil, fmt.Errorf("ambiguous native output envelope")
	}
	data := selected
	if nested, exists := selected["data"]; exists {
		data, ok = nested.(map[string]any)
		if !ok || len(data) != 1 {
			return nil, fmt.Errorf("invalid native safe-output envelope")
		}
	}
	digest, ok := data["request_sha256"].(string)
	if !ok || !nativeCompilerHash.MatchString(digest) {
		return nil, fmt.Errorf("replacement request digest must be lowercase SHA256")
	}
	directory, err := unix.Open(filepath.Join(filepath.Dir(outputFile), "agent"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open native artifact directory: %w", err)
	}
	defer unix.Close(directory)
	payload, err := nativePublicationReadFile(nativeRequestFile, directory)
	if err != nil {
		return nil, fmt.Errorf("read native request artifact: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("replacement request artifact digest mismatch")
	}
	value, err = nativeDecode(payload)
	if err != nil {
		return nil, fmt.Errorf("decode native request: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 5 {
		return nil, fmt.Errorf("invalid replacement schema")
	}
	request := nativePublicationRequest{}
	request.ExpectedBaseSHA, ok = object["expected_base_sha"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid replacement base")
	}
	request.Patch, ok = object["patch"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid replacement patch")
	}
	request.Title, ok = object["title"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid replacement title")
	}
	request.Body, ok = object["body"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid replacement body")
	}
	sources, ok := object["source_prs"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing source PRs")
	}
	for _, raw := range sources {
		source, ok := raw.(map[string]any)
		if !ok || len(source) != 2 {
			return nil, fmt.Errorf("invalid source schema")
		}
		number, valid := nativeInteger(source["number"])
		sha, validSHA := source["head_sha"].(string)
		if !valid || number <= 0 || int64(int(number)) != number || !validSHA {
			return nil, fmt.Errorf("invalid source identity")
		}
		request.SourcePRs = append(request.SourcePRs, nativePublicationSource{Number: int(number), HeadSHA: sha})
	}
	if _, err := nativeValidatePublicationRequest(request); err != nil {
		return nil, err
	}
	return &NativePublicationRequest{request: request}, nil
}

// Native publication file reads reject links, special files and unbounded input.
func nativePublicationReadFile(path string, directory int) ([]byte, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if directory == -1 {
		directory = unix.AT_FDCWD
	}
	fd, err := unix.Openat(directory, path, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat publication file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > nativeMaxRequest {
		return nil, fmt.Errorf("publication input must be a bounded regular file")
	}
	payload, err := io.ReadAll(io.LimitReader(file, nativeMaxRequest+1))
	if err != nil {
		return nil, fmt.Errorf("read publication file: %w", err)
	}
	if len(payload) == 0 || len(payload) > nativeMaxRequest || !utf8.Valid(payload) {
		return nil, fmt.Errorf("publication input must be bounded UTF-8")
	}
	if err := nativePublicationJSONUnicode(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// Native request validation rejects every local input error before dependencies are used.
func nativeValidatePublicationRequest(request nativePublicationRequest) ([]string, error) {
	if !nativeSHA.MatchString(request.ExpectedBaseSHA) {
		return nil, fmt.Errorf("expected base must be a full lowercase commit SHA")
	}
	if len(request.SourcePRs) == 0 || len(request.SourcePRs) > 100 {
		return nil, fmt.Errorf("missing source PRs")
	}
	previous := 0
	for _, source := range request.SourcePRs {
		if source.Number <= previous || !nativeSHA.MatchString(source.HeadSHA) {
			return nil, fmt.Errorf("source PRs require sorted unique positive numbers and full lowercase SHAs")
		}
		previous = source.Number
	}
	if _, err := nativeSafeText(request.Title, true); err != nil {
		return nil, fmt.Errorf("validate publication title: %w", err)
	}
	if _, err := nativeSafeText(request.Body, false); err != nil {
		return nil, fmt.Errorf("validate publication body: %w", err)
	}
	paths, err := nativePatchPaths(request.Patch)
	if err != nil {
		return nil, fmt.Errorf("validate publication patch paths: %w", err)
	}
	if len(paths) >= automation.MaxPublicationFiles {
		return nil, fmt.Errorf("publication patch must contain fewer than %d files", automation.MaxPublicationFiles)
	}
	return paths, nil
}
