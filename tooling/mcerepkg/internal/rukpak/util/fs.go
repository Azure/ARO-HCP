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

package util

import (
	"io/fs"
	"os"
)

// FilesOnlyFilesystem is an fs.FS implementation that treats non-regular files
// (e.g. directories, symlinks, devices, etc.) as non-existent. The reason for
// this is so that we only serve bundle files.
//
// This treats directories as not found so that the http server does not serve
// HTML directory index responses.
//
// This treats other symlink files as not found so that we prevent HTTP requests
// from escaping the filesystem root.
//
// Lastly, this treats other non-regular files as not found because they are
// out of scope for serving bundle contents.
type FilesOnlyFilesystem struct {
	FS fs.FS
}

func (f *FilesOnlyFilesystem) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	return file, nil
}
