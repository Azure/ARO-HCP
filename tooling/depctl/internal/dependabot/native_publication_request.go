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

// Native publication snapshots retain the envelope and file identity used to validate the request.
type nativePublicationArtifact struct {
	envelope map[string]any
	selected map[string]any
	request  *NativePublicationRequest
	original unix.Stat_t
}

// ReadNativePublicationRequest validates the envelope and exact artifact before any authenticated work.
func ReadNativePublicationRequest(outputFile string) (*NativePublicationRequest, error) {
	snapshot, err := nativeReadPublicationSnapshot(outputFile)
	if err != nil || snapshot == nil {
		return nil, err
	}
	return snapshot.request, nil
}

// Native publication loading binds the artifact and selected output to one envelope snapshot.
func nativeReadPublicationSnapshot(outputFile string) (*nativePublicationArtifact, error) {
	outputFile = filepath.Clean(outputFile)
	directory, err := unix.Open(filepath.Dir(outputFile), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open native output directory: %w", err)
	}
	defer unix.Close(directory)
	envelope, original, err := nativeReadOutputEnvelope(directory, filepath.Base(outputFile))
	if err != nil {
		return nil, fmt.Errorf("read publication envelope: %w", err)
	}
	selected, digest, err := nativeSelectOutput(envelope, "native_replacement", "request_sha256")
	if err != nil {
		return nil, fmt.Errorf("select native replacement: %w", err)
	}
	if selected == nil {
		return nil, nil
	}
	if !nativeCompilerHash.MatchString(digest) {
		return nil, fmt.Errorf("replacement request digest must be lowercase SHA256")
	}
	request, err := nativeReadPublicationArtifact(directory, digest)
	if err != nil {
		return nil, err
	}
	return &nativePublicationArtifact{envelope: envelope, selected: selected, request: request, original: original}, nil
}

// Native output envelope reading binds parsed data to the original regular file identity.
func nativeReadOutputEnvelope(directory int, name string) (map[string]any, unix.Stat_t, error) {
	file, err := nativePublicationOpenFile(directory, name, unix.O_RDONLY)
	if err != nil {
		return nil, unix.Stat_t{}, fmt.Errorf("open native output: %w", err)
	}
	defer file.Close()
	output, original, err := nativePublicationReadFile(file)
	if err != nil {
		return nil, original, fmt.Errorf("read native output: %w", err)
	}
	value, err := nativeDecode(output)
	if err != nil {
		return nil, original, fmt.Errorf("decode native output: %w", err)
	}
	envelope, ok := value.(map[string]any)
	if !ok {
		return nil, original, fmt.Errorf("invalid global agent output")
	}
	return envelope, original, nil
}

// Native output selection rejects mixed native actions and ambiguous payload identities.
func nativeSelectOutput(envelope map[string]any, kind, field string) (map[string]any, string, error) {
	items, ok := envelope["items"].([]any)
	if !ok {
		return nil, "", fmt.Errorf("invalid global agent output items")
	}
	if errors, exists := envelope["errors"]; exists {
		list, ok := errors.([]any)
		if !ok || len(list) != 0 {
			return nil, "", fmt.Errorf("agent output contains errors")
		}
	}
	var selected map[string]any
	nativeItems := 0
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("malformed output item")
		}
		itemKind, ok := item["type"].(string)
		if !ok {
			return nil, "", fmt.Errorf("malformed output item type")
		}
		if itemKind == "native_replacement" || itemKind == "native_action" {
			nativeItems++
		}
		if itemKind == kind {
			selected = item
		}
	}
	if nativeItems > 1 {
		return nil, "", fmt.Errorf("only one native output kind/item may run per workflow")
	}
	if selected == nil {
		return nil, "", nil
	}
	// The safe-output runtime attaches an opaque receipt ID outside the payload.
	fields := len(selected)
	if rawID, exists := selected["output_id"]; exists {
		id, ok := rawID.(string)
		if !ok || id == "" {
			return nil, "", fmt.Errorf("invalid native output ID")
		}
		fields--
	}
	if fields != 2 {
		return nil, "", fmt.Errorf("ambiguous native output envelope")
	}
	data := selected
	if nested, exists := selected["data"]; exists {
		data, ok = nested.(map[string]any)
		if !ok || len(data) != 1 {
			return nil, "", fmt.Errorf("invalid native safe-output envelope")
		}
	}
	payload, ok := data[field].(string)
	if !ok {
		return nil, "", fmt.Errorf("invalid native safe-output payload")
	}
	return selected, payload, nil
}

// Native artifact loading verifies the exact bytes before decoding and validating a request.
func nativeReadPublicationArtifact(outputDirectory int, digest string) (*NativePublicationRequest, error) {
	directory, err := unix.Openat(outputDirectory, "agent", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open native artifact directory: %w", err)
	}
	defer unix.Close(directory)
	file, err := nativePublicationOpenFile(directory, nativeRequestFile, unix.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("open native request artifact: %w", err)
	}
	defer file.Close()
	payload, _, err := nativePublicationReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read native request artifact: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("replacement request artifact digest mismatch")
	}
	value, err := nativeDecode(payload)
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

// Native publication file opens never follow the named entry or block on a FIFO.
func nativePublicationOpenFile(directory int, name string, access int) (*os.File, error) {
	fd, err := unix.Openat(directory, name, access|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Native publication file reads reject hard links, special files and unbounded input.
func nativePublicationReadFile(file *os.File) ([]byte, unix.Stat_t, error) {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return nil, info, fmt.Errorf("stat publication file: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 || info.Size <= 0 || info.Size > nativeMaxRequest {
		return nil, info, fmt.Errorf("publication input must be a bounded, singly linked regular file")
	}
	payload, err := io.ReadAll(io.LimitReader(file, nativeMaxRequest+1))
	if err != nil {
		return nil, info, fmt.Errorf("read publication file: %w", err)
	}
	if len(payload) == 0 || len(payload) > nativeMaxRequest || !utf8.Valid(payload) {
		return nil, info, fmt.Errorf("publication input must be bounded UTF-8")
	}
	if err := nativePublicationJSONUnicode(payload); err != nil {
		return nil, info, err
	}
	return payload, info, nil
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
