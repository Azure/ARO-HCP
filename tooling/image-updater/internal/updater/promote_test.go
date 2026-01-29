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
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/testutil"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

type mockEditor map[string]struct {
	lineNumber int
	value      string
	rawLine    string
}

func (me *mockEditor) GetUpdate(path string) (int, string, error) {
	return (*me)[path].lineNumber, (*me)[path].value, nil
}
func (me *mockEditor) GetLineWithComment(path string) (int, string, string, error) {
	return (*me)[path].lineNumber, (*me)[path].value, (*me)[path].rawLine, nil
}
func (me *mockEditor) ApplyUpdates(updates []yaml.Update) error { return nil }

func TestPromotion_Build(t *testing.T) {
	tests := []struct {
		name        string
		Promotion   *Promotion
		getEditor   editorGetter
		wantErr     bool
		wantErrKind PromotionBuildErrorKind
	}{
		{
			name: "no sources -> PromotionBuildErrNoSources",
			Promotion: &Promotion{
				ImageName:              "img",
				SourceImageDeclaration: nil,
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "x", JSONPath: "y"},
				},
			},
			getEditor:   func(string) (yaml.EditorInterface, error) { return &mockEditor{}, nil },
			wantErr:     true,
			wantErrKind: PromotionBuildErrNoSources,
		},
		{
			name: "no targets -> PromotionBuildErrNoTargets",
			Promotion: &Promotion{
				ImageName:              "img",
				SourceImageDeclaration: []*ImageDeclaration{{FilePath: "x", JSONPath: "y"}},
				TargetImageDeclaration: nil,
			},
			getEditor:   func(string) (yaml.EditorInterface, error) { return &mockEditor{}, nil },
			wantErr:     true,
			wantErrKind: PromotionBuildErrNoTargets,
		},
		{
			name: "getEditor error",
			Promotion: &Promotion{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{FilePath: "missing.yaml", JSONPath: "sources.a"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "missing.yaml", JSONPath: "targets.t1"},
				},
			},
			getEditor:   func(string) (yaml.EditorInterface, error) { return nil, errors.New("error") },
			wantErr:     true,
			wantErrKind: PromotionBuildErrOther,
		},
		{
			name: "empty source digest -> PromotionBuildErrEmptySourceDigest",
			Promotion: &Promotion{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "sources.a"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "targets.t1"},
				},
			},
			getEditor: func(string) (yaml.EditorInterface, error) {
				return &mockEditor{
					"sources.a": {
						lineNumber: 1,
						value:      "",
						rawLine:    `a: "" # v1 (2026-01-01 00:00)`,
					},
					"targets.t1": {
						lineNumber: 2,
						value:      "sha256:abc123",
						rawLine:    "t1: sha256:abc123",
					},
				}, nil
			},
			wantErr:     true,
			wantErrKind: PromotionBuildErrEmptySourceDigest,
		},
		{
			name: "diverging sources -> PromotionBuildErrDivergingSources",
			Promotion: &Promotion{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "sources.a"},
					{FilePath: "test.yaml", JSONPath: "sources.b"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "targets.t1"},
				},
			},
			getEditor: func(string) (yaml.EditorInterface, error) {
				return &mockEditor{
					"sources.a": {
						lineNumber: 1,
						value:      "sha256:one",
						rawLine:    `a: sha256:one # v1 (2026-01-01 00:00)`,
					},
					"sources.b": {
						lineNumber: 2,
						value:      "sha256:two",
						rawLine:    `b: sha256:two # v1 (2026-01-01 00:00)`,
					},
					"targets.t1": {
						lineNumber: 3,
						value:      "sha256:abc123",
						rawLine:    "t1: sha256:abc123",
					},
				}, nil
			},
			wantErr:     true,
			wantErrKind: PromotionBuildErrDivergingSources,
		},
		{
			name: "success populates digests, lines, and source tag/date",
			Promotion: &Promotion{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "sources.a"},
					{FilePath: "test.yaml", JSONPath: "sources.b"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "test.yaml", JSONPath: "targets.t1"},
					{FilePath: "test.yaml", JSONPath: "targets.t2"},
				},
			},
			getEditor: func(string) (yaml.EditorInterface, error) {
				return &mockEditor{
					"sources.a": {
						lineNumber: 1,
						value:      "sha256:same",
						rawLine:    "a: sha256:same # vX (2026-01-01 00:00)",
					},
					"sources.b": {
						lineNumber: 2,
						value:      "sha256:same",
						rawLine:    "b: sha256:same # vX (2026-01-01 00:00)",
					},
					"targets.t1": {
						lineNumber: 3,
						value:      "sha256:old",
						rawLine:    "t1: sha256:old",
					},
					"targets.t2": {
						lineNumber: 4,
						value:      "sha256:same",
						rawLine:    "t2: sha256:same",
					},
				}, nil
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			gotErr := tt.Promotion.Build(tt.getEditor)
			if tt.wantErr {
				if gotErr == nil {
					t.Fatal("Build() succeeded unexpectedly")
				}
				if !errors.Is(gotErr, &PromotionBuildError{Kind: tt.wantErrKind}) {
					t.Fatalf("Build() error = %v, want kind %v", gotErr, tt.wantErrKind)
				}
			} else {
				if gotErr != nil {
					t.Fatalf("Build() failed: %v", gotErr)
				}
				serialized, err := yamlv3.Marshal(tt.Promotion)
				if err != nil {
					t.Fatalf("failed to marshal promotion: %v", err)
				}
				testutil.CompareWithFixture(t, serialized, testutil.WithExtension(".yaml"))
			}
		})
	}
}

func TestPromotionBuildError_Error(t *testing.T) {
	type fields struct {
		Kind     PromotionBuildErrorKind
		Image    string
		FilePath string
		JSONPath string
		Expected string
		Got      string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "no sources",
			fields: fields{
				Kind:  PromotionBuildErrNoSources,
				Image: "img",
			},
			want: "no sources found for promotion: img",
		},
		{
			name: "no targets",
			fields: fields{
				Kind:  PromotionBuildErrNoTargets,
				Image: "img",
			},
			want: "no targets found for promotion: img",
		},
		{
			name: "empty digest",
			fields: fields{
				Kind:     PromotionBuildErrEmptySourceDigest,
				Image:    "img",
				FilePath: "f.yaml",
				JSONPath: "a.b",
			},
			want: "empty source digest for promotion: img (f.yaml:a.b)",
		},
		{
			name: "diverging sources",
			fields: fields{
				Kind:     PromotionBuildErrDivergingSources,
				Image:    "img",
				FilePath: "f.yaml",
				JSONPath: "a.b",
				Expected: "sha256:one",
				Got:      "sha256:two",
			},
			want: "diverging source images found for promotion: img (expected \"sha256:one\", got \"sha256:two\" at f.yaml:a.b)",
		},
		{
			name: "other",
			fields: fields{
				Kind:  PromotionBuildErrOther,
				Image: "img",
			},
			want: "promotion build error for img: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &PromotionBuildError{
				Kind:     tt.fields.Kind,
				Image:    tt.fields.Image,
				FilePath: tt.fields.FilePath,
				JSONPath: tt.fields.JSONPath,
				Expected: tt.fields.Expected,
				Got:      tt.fields.Got,
				WrappedError: func() error {
					if tt.fields.Kind == PromotionBuildErrOther {
						return errors.New("boom")
					}
					return nil
				}(),
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("PromotionBuildError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSkippablePromotionBuildError(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "skippable no sources",
			args: args{err: &PromotionBuildError{Kind: PromotionBuildErrNoSources, Image: "img"}},
			want: true,
		},
		{
			name: "skippable no targets",
			args: args{err: &PromotionBuildError{Kind: PromotionBuildErrNoTargets, Image: "img"}},
			want: true,
		},
		{
			name: "skippable empty digest",
			args: args{err: &PromotionBuildError{Kind: PromotionBuildErrEmptySourceDigest, Image: "img", FilePath: "f", JSONPath: "p"}},
			want: true,
		},
		{
			name: "skippable diverging sources",
			args: args{err: &PromotionBuildError{Kind: PromotionBuildErrDivergingSources, Image: "img", FilePath: "f", JSONPath: "p", Expected: "a", Got: "b"}},
			want: true,
		},
		{
			name: "wrapped skippable",
			args: args{err: fmt.Errorf("wrap: %w", &PromotionBuildError{Kind: PromotionBuildErrNoTargets, Image: "img"})},
			want: true,
		},
		{
			name: "non-skippable other kind",
			args: args{err: &PromotionBuildError{Kind: PromotionBuildErrOther, Image: "img", WrappedError: errors.New("boom")}},
			want: false,
		},
		{
			name: "non-skippable non-promotion error",
			args: args{err: errors.New("boom")},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSkippablePromotionBuildError(tt.args.err); got != tt.want {
				t.Errorf("IsSkippablePromotionBuildError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromotion_Execute(t *testing.T) {
	type fields struct {
		SourceImageDeclaration []*ImageDeclaration
		TargetImageDeclaration []*ImageDeclaration
		ImageName              string
	}
	type args struct {
		ctx         context.Context
		forceUpdate bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []yaml.Update
	}{
		{
			name: "no updates when digests match and not forced",
			fields: fields{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{Digest: "sha256:same", VersionTag: "v1", VersionDate: "2026-01-01 00:00"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "f.yaml", JSONPath: "targets.t1", Digest: "sha256:same", Line: 10},
				},
			},
			args: args{ctx: logr.NewContext(context.Background(), logr.Discard()), forceUpdate: false},
			want: []yaml.Update{},
		},
		{
			name: "forced update when digests match",
			fields: fields{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{Digest: "sha256:same", VersionTag: "v1", VersionDate: "2026-01-01 00:00"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "f.yaml", JSONPath: "targets.t1", Digest: "sha256:same", Line: 10},
				},
			},
			args: args{ctx: logr.NewContext(context.Background(), logr.Discard()), forceUpdate: true},
			want: []yaml.Update{
				{
					Name:      "img",
					NewDigest: "sha256:same",
					OldDigest: "sha256:same",
					Tag:       "v1",
					Date:      "2026-01-01 00:00",
					JsonPath:  "targets.t1",
					FilePath:  "f.yaml",
					Line:      10,
				},
			},
		},
		{
			name: "promotion needed when digests differ (multiple targets/files)",
			fields: fields{
				ImageName: "img",
				SourceImageDeclaration: []*ImageDeclaration{
					{Digest: "sha256:new", VersionTag: "v2", VersionDate: "2026-01-02 00:00"},
				},
				TargetImageDeclaration: []*ImageDeclaration{
					{FilePath: "f.yaml", JSONPath: "targets.t1", Digest: "sha256:old", Line: 9},
					{FilePath: "g.yaml", JSONPath: "targets.t2", Digest: "sha256:older", Line: 7},
				},
			},
			args: args{ctx: logr.NewContext(context.Background(), logr.Discard()), forceUpdate: false},
			want: []yaml.Update{
				{
					Name:      "img",
					NewDigest: "sha256:new",
					OldDigest: "sha256:old",
					Tag:       "v2",
					Date:      "2026-01-02 00:00",
					JsonPath:  "targets.t1",
					FilePath:  "f.yaml",
					Line:      9,
				},
				{
					Name:      "img",
					NewDigest: "sha256:new",
					OldDigest: "sha256:older",
					Tag:       "v2",
					Date:      "2026-01-02 00:00",
					JsonPath:  "targets.t2",
					FilePath:  "g.yaml",
					Line:      7,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Promotion{
				SourceImageDeclaration: tt.fields.SourceImageDeclaration,
				TargetImageDeclaration: tt.fields.TargetImageDeclaration,
				ImageName:              tt.fields.ImageName,
			}
			got, err := p.Execute(tt.args.ctx, tt.args.forceUpdate)
			if err != nil {
				t.Fatalf("Promotion.Execute() returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Promotion.Execute() = %v, want %v", got, tt.want)
			}
		})
	}
}
