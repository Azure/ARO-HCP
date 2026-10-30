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

package types

import (
	"fmt"
	"slices"

	_ "embed"
)

const StepActionImageMirror = "ImageMirror"

//go:embed on-demand.sh
var OnDemandSyncScript []byte

// ImageMirrorStep is a handy wrapper over a ShellStep that allows many users of this tooling to mirror images in the
// same way without having to worry about the shell script itself.
type ImageMirrorStep struct {
	StepMeta `json:",inline"`

	TargetACR             Value  `json:"targetACR,omitempty"`
	SourceRegistry        Value  `json:"sourceRegistry,omitempty"`
	Repository            Value  `json:"repository,omitempty"`
	Digest                Value  `json:"digest,omitempty"`
	CopyFrom              string `json:"copyFrom,omitempty"`
	PublicSource          bool   `json:"publicSource,omitempty"`
	ImageFilePath         Value  `json:"imageFilePath,omitempty"` // optional, if path is same as pipeline.yaml
	ImageTarFileName      Value  `json:"imageTarFileName,omitempty"`
	ImageMetadataFileName Value  `json:"imageMetadataFileName,omitempty"`
	PullSecretKeyVault    Value  `json:"pullSecretKeyVault,omitempty"`
	PullSecretName        Value  `json:"pullSecretName,omitempty"`
	ShellIdentity         Value  `json:"shellIdentity,omitempty"`
	ADOProject            string `json:"adoProject,omitempty"`
	ArtifactName          string `json:"artifactName,omitempty"`
	BuildID               string `json:"buildId,omitempty"`
}

func (s *ImageMirrorStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  From %v:%v@%v to %v\n", s.Name, s.Action, s.SourceRegistry, s.Repository, s.Digest, s.TargetACR)
}

func (s *ImageMirrorStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.TargetACR, s.SourceRegistry, s.Repository, s.Digest, s.ImageFilePath, s.ImageTarFileName, s.ImageMetadataFileName, s.PullSecretKeyVault, s.PullSecretName, s.ShellIdentity} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

// ResolveImageMirrorStep resolves an image mirror step to a shell step. It's up to the user to write the contents of
// the OnDemandSyncScript to disk somewhere and pass the file name in as a parameter here, as we likely don't want to
// inline 100+ lines of shell into a `bash -C "<contents>"` call and hope all the string interpolations work.
func ResolveImageMirrorStep(input ImageMirrorStep, scriptFile string) (*ShellStep, error) {
	variables := []Variable{
		namedVariable("TARGET_ACR", input.TargetACR),
		namedVariable("REPOSITORY", input.Repository),
	}

	switch input.CopyFrom {
	case "oci-layout":
		variables = append(variables, namedVariable("IMAGE_FILE_PATH", input.ImageFilePath))
		variables = append(variables, namedVariable("IMAGE_TAR_FILE_NAME", input.ImageTarFileName))
		variables = append(variables, namedVariable("IMAGE_METADATA_FILE_NAME", input.ImageMetadataFileName))
	default:
		variables = append(variables, namedVariable("SOURCE_REGISTRY", input.SourceRegistry))
		variables = append(variables, namedVariable("DIGEST", input.Digest))
		if !input.PublicSource {
			variables = append(variables, namedVariable("PULL_SECRET_KV", input.PullSecretKeyVault))
			variables = append(variables, namedVariable("PULL_SECRET", input.PullSecretName))
		}
	}

	return &ShellStep{
		StepMeta: StepMeta{
			Name:      input.Name,
			Action:    "Shell",
			DependsOn: input.DependsOn,
		},
		Command:   fmt.Sprintf("/bin/bash %s", scriptFile),
		Variables: variables,
		DryRun: DryRun{
			Variables: []Variable{{
				Name: "DRY_RUN",
				Value: Value{
					Value: "true",
				},
			}},
		},
		ShellIdentity: input.ShellIdentity,
	}, nil
}

func namedVariable(name string, value Value) Variable {
	return Variable{
		Name: name,
		Value: Value{
			Value:     value.Value,
			ConfigRef: value.ConfigRef,
			Input:     value.Input,
		},
	}
}
