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

package api

import (
	"reflect"
	"sync"
)

type StructTagMap map[string]reflect.StructTag

var structTagMapByType map[reflect.Type]StructTagMap = map[reflect.Type]StructTagMap{}
var structTagMapByTypeMutex sync.RWMutex

func join(ns, name string) string {
	res := ns
	if res != "" {
		res += "."
	}
	res += name
	return res
}

func buildStructTagMap(m StructTagMap, t reflect.Type, path string) {
	switch t.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		buildStructTagMap(m, t.Elem(), path)

	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			var subpath string

			field := t.Field(i)

			if !field.IsExported() {
				continue
			}

			// Omit embedded field names from the key.
			// Generated API structs do not have them.
			if field.Anonymous {
				subpath = path
			} else {
				subpath = join(path, field.Name)
				m[subpath] = field.Tag
			}

			buildStructTagMap(m, field.Type, subpath)
		}
	}
}

// GetStructTagMap returns a mapping of dot-separated struct field
// names (with no subscripts) to struct tags for the given type.
func GetStructTagMap[T any]() StructTagMap {
	t := reflect.TypeFor[T]()

	structTagMapByTypeMutex.RLock()
	m, found := structTagMapByType[t]
	structTagMapByTypeMutex.RUnlock()

	if found {
		return m
	}

	// Promote to exclusive lock and check again.
	structTagMapByTypeMutex.Lock()
	defer structTagMapByTypeMutex.Unlock()
	m, found = structTagMapByType[t]

	if !found {
		m = StructTagMap{}
		buildStructTagMap(m, t, "")
		structTagMapByType[t] = m
	}

	return m
}
