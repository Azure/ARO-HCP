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

package clients

import (
	"context"
	"time"
)

// RegistryClient defines the interface for container registry clients
type RegistryClient interface {
	GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool) (*ImageInfo, error)
}

type Tag struct {
	Name         string
	Digest       string
	LastModified time.Time
}

// ImageInfo holds the digest and tag information for an image
type ImageInfo struct {
	Digest string
	Tag    string
}
