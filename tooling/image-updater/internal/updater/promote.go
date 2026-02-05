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

package updater

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

type PromotionBuildErrorKind int

const (
	PromotionBuildErrNoSources PromotionBuildErrorKind = iota
	PromotionBuildErrNoTargets
	PromotionBuildErrEmptySourceDigest
	PromotionBuildErrDivergingSources
	PromotionBuildErrOther
)

type PromotionBuildError struct {
	Kind         PromotionBuildErrorKind
	Image        string
	FilePath     string
	JSONPath     string
	Expected     string
	Got          string
	WrappedError error
}

func (e *PromotionBuildError) Error() string {
	switch e.Kind {
	case PromotionBuildErrNoSources:
		return fmt.Sprintf("no sources found for promotion: %s", e.Image)
	case PromotionBuildErrNoTargets:
		return fmt.Sprintf("no targets found for promotion: %s", e.Image)
	case PromotionBuildErrEmptySourceDigest:
		return fmt.Sprintf("empty source digest for promotion: %s (%s:%s)", e.Image, e.FilePath, e.JSONPath)
	case PromotionBuildErrDivergingSources:
		return fmt.Sprintf("diverging source images found for promotion: %s (expected %q, got %q at %s:%s)",
			e.Image, e.Expected, e.Got, e.FilePath, e.JSONPath)
	default:
		return fmt.Sprintf("promotion build error for %s: %v", e.Image, e.WrappedError)
	}
}

func (e *PromotionBuildError) Is(target error) bool {
	if e == nil {
		return false
	}
	t, ok := target.(*PromotionBuildError)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

func IsSkippablePromotionBuildError(err error) bool {
	var e *PromotionBuildError
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case PromotionBuildErrNoSources,
		PromotionBuildErrNoTargets,
		PromotionBuildErrEmptySourceDigest,
		PromotionBuildErrDivergingSources:
		return true
	default:
		return false
	}
}

type ImageDeclaration struct {
	JSONPath    string
	FilePath    string
	Digest      string
	Line        int
	VersionTag  string
	VersionDate string
	ValueType   string
}

type editorGetter func(string) (yaml.EditorInterface, error)

type Promotion struct {
	SourceImageDeclaration []*ImageDeclaration
	TargetImageDeclaration []*ImageDeclaration
	ImageName              string
}

func (p *Promotion) Build(getEditor editorGetter) error {
	if len(p.SourceImageDeclaration) == 0 {
		return &PromotionBuildError{Kind: PromotionBuildErrNoSources, Image: p.ImageName}
	}
	if len(p.TargetImageDeclaration) == 0 {
		return &PromotionBuildError{Kind: PromotionBuildErrNoTargets, Image: p.ImageName}
	}

	var imageToPromote string
	for i, source := range p.SourceImageDeclaration {

		editor, err := getEditor(source.FilePath)
		if err != nil {
			return &PromotionBuildError{Kind: PromotionBuildErrOther, Image: p.ImageName,
				FilePath: source.FilePath, WrappedError: err}
		}

		line, sourceDigest, lineContent, err := editor.GetLineWithComment(source.JSONPath)
		if err != nil {
			return &PromotionBuildError{Kind: PromotionBuildErrOther, Image: p.ImageName,
				FilePath: source.FilePath, JSONPath: source.JSONPath, WrappedError: err}
		}
		if sourceDigest == "" {
			return &PromotionBuildError{
				Kind:     PromotionBuildErrEmptySourceDigest,
				Image:    p.ImageName,
				FilePath: source.FilePath,
				JSONPath: source.JSONPath,
			}
		}
		source.Digest = sourceDigest
		source.Line = line
		source.VersionTag, source.VersionDate = yaml.ParseVersionComment(lineContent)

		if i == 0 {
			imageToPromote = sourceDigest
			continue
		}
		if imageToPromote != sourceDigest {
			return &PromotionBuildError{
				Kind:     PromotionBuildErrDivergingSources,
				Image:    p.ImageName,
				FilePath: source.FilePath,
				JSONPath: source.JSONPath,
				Expected: imageToPromote,
				Got:      sourceDigest,
			}
		}
	}

	for _, target := range p.TargetImageDeclaration {
		editor, err := getEditor(target.FilePath)
		if err != nil {
			return &PromotionBuildError{Kind: PromotionBuildErrOther, Image: p.ImageName,
				FilePath: target.FilePath, WrappedError: err}
		}
		line, targetDigest, err := editor.GetUpdate(target.JSONPath)
		if err != nil {
			return &PromotionBuildError{Kind: PromotionBuildErrOther, Image: p.ImageName,
				FilePath: target.FilePath, JSONPath: target.JSONPath, WrappedError: err}
		}
		target.Digest = targetDigest
		target.Line = line
	}

	return nil
}

func (p *Promotion) Execute(ctx context.Context, forceUpdate bool) ([]yaml.Update, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	// All source digests are the same, just pick the first one
	// This is validated in Build()
	imageToPromote := p.SourceImageDeclaration[0].Digest
	tag := p.SourceImageDeclaration[0].VersionTag
	date := p.SourceImageDeclaration[0].VersionDate

	updates := make([]yaml.Update, 0, len(p.TargetImageDeclaration))
	for _, target := range p.TargetImageDeclaration {

		switch {
		case target.Digest == imageToPromote && !forceUpdate:
			logger.V(2).Info("No update needed - digests match",
				"name", p.ImageName,
				"jsonPath", target.JSONPath,
				"filePath", target.FilePath)
			continue

		case target.Digest == imageToPromote && forceUpdate:
			logger.V(1).Info("Force update - regenerating comments",
				"name", p.ImageName,
				"jsonPath", target.JSONPath,
				"filePath", target.FilePath)

		case target.Digest != imageToPromote:
			logger.V(1).Info("Promotion needed",
				"name", p.ImageName,
				"jsonPath", target.JSONPath,
				"filePath", target.FilePath,
				"from", target.Digest,
				"to", imageToPromote)
		}

		updates = append(updates, yaml.Update{
			Name:      p.ImageName,
			NewDigest: imageToPromote,
			OldDigest: target.Digest,
			Tag:       tag,
			Date:      date,
			JsonPath:  target.JSONPath,
			FilePath:  target.FilePath,
			Line:      target.Line,
			ValueType: target.ValueType,
		})
	}
	return updates, nil
}
