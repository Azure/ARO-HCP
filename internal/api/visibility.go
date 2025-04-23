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
	"fmt"
	"reflect"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Property visibility meanings:
// https://azure.github.io/typespec-azure/docs/howtos/ARM/resource-type#property-visibility-and-other-constraints
//
// Field mutability guidelines:
// https://github.com/microsoft/api-guidelines/blob/vNext/azure/Guidelines.md#resource-schema--field-mutability

const VisibilityStructTagKey = "visibility"

// VisibilityFlags holds a visibility struct tag value as bit flags.
type VisibilityFlags uint8

const (
	VisibilityRead VisibilityFlags = 1 << iota
	VisibilityCreate
	VisibilityUpdate

	// option flags
	VisibilityCaseInsensitive

	VisibilityDefault = VisibilityRead | VisibilityCreate | VisibilityUpdate
)

func (f VisibilityFlags) ReadOnly() bool {
	return f&(VisibilityRead|VisibilityCreate|VisibilityUpdate) == VisibilityRead
}

func (f VisibilityFlags) CanUpdate() bool {
	return f&VisibilityUpdate != 0
}

func (f VisibilityFlags) CaseInsensitive() bool {
	return f&VisibilityCaseInsensitive != 0
}

func (f VisibilityFlags) String() string {
	s := []string{}
	if f&VisibilityRead != 0 {
		s = append(s, "read")
	}
	if f&VisibilityCreate != 0 {
		s = append(s, "create")
	}
	if f&VisibilityUpdate != 0 {
		s = append(s, "update")
	}
	if f&VisibilityCaseInsensitive != 0 {
		s = append(s, "nocase")
	}
	return strings.Join(s, " ")
}

func GetVisibilityFlags(tag reflect.StructTag) (VisibilityFlags, bool) {
	var flags VisibilityFlags

	tagValue, ok := tag.Lookup(VisibilityStructTagKey)
	if ok {
		for _, v := range strings.Fields(tagValue) {
			switch strings.ToLower(v) {
			case "read":
				flags |= VisibilityRead
			case "create":
				flags |= VisibilityCreate
			case "update":
				flags |= VisibilityUpdate
			case "nocase":
				flags |= VisibilityCaseInsensitive
			default:
				panic(fmt.Sprintf("Unknown visibility tag value '%s'", v))
			}
		}
	}

	return flags, ok
}

func join(ns, name string) string {
	res := ns
	if res != "" {
		res += "."
	}
	res += name
	return res
}

type StructTagMap map[string]reflect.StructTag

func buildStructTagMap(structTagMap StructTagMap, t reflect.Type, path string) {
	switch t.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		buildStructTagMap(structTagMap, t.Elem(), path)

	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			subpath := join(path, field.Name)

			if len(field.Tag) > 0 {
				structTagMap[subpath] = field.Tag
			}

			buildStructTagMap(structTagMap, field.Type, subpath)
		}
	}
}

// NewStructTagMap returns a mapping of dot-separated struct field names
// to struct tags for the given type.  Each versioned API should create
// its own visibiilty map for tracked resource types.
//
// Note: This assumes field names for internal and versioned structs are
// identical where visibility is explicitly specified. If some divergence
// emerges, one workaround could be to pass a field name override map.
func NewStructTagMap[T any]() StructTagMap {
	structTagMap := StructTagMap{}
	buildStructTagMap(structTagMap, reflect.TypeFor[T](), "")
	return structTagMap
}

type validateVisibility struct {
	structTagMap StructTagMap
	updating     bool
	errs         []arm.CloudErrorBody
}

// ValidateVisibility compares the new value (newVal) to the current value
// (curVal) and returns any violations of visibility restrictions as defined
// by structTagMap.
func ValidateVisibility(newVal, curVal interface{}, structTagMap StructTagMap, updating bool) []arm.CloudErrorBody {
	vv := validateVisibility{
		structTagMap: structTagMap,
		updating:     updating,
	}
	vv.recurse(reflect.ValueOf(newVal), reflect.ValueOf(curVal), "", "", "", VisibilityDefault)
	return vv.errs
}

// mapKey is a lookup key for the StructTagMap.  It DOES NOT include subscripts
// for arrays, maps or slices since all elements are the same type.
//
// namespace is the struct field path up to but not including the field being
// evaluated, analogous to path.Dir.  It DOES include subscripts for arrays,
// maps and slices since its purpose is for error reporting.
//
// fieldname is the current field being evaluated, analgous to path.Base.  It
// also includes subscripts for arrays, maps and slices when evaluating their
// immediate elements.
func (vv *validateVisibility) recurse(newVal, curVal reflect.Value, mapKey, namespace, fieldname string, implicitVisibility VisibilityFlags) {
	flags, ok := GetVisibilityFlags(vv.structTagMap[mapKey])
	if !ok {
		flags = implicitVisibility
	}

	if newVal.Type() != curVal.Type() {
		panic(fmt.Sprintf("%s: value types differ (%s vs %s)", join(namespace, fieldname), newVal.Type().Name(), curVal.Type().Name()))
	}

	// Generated API structs are all pointer fields. A nil pointer in
	// the incoming request (newVal) means the value is absent, which
	// is always acceptable for visibility validation.
	switch newVal.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if newVal.IsNil() {
			return
		}
	}

	switch newVal.Kind() {
	case reflect.Bool:
		if newVal.Bool() != curVal.Bool() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if newVal.Int() != curVal.Int() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Uint, reflect.Uintptr, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if newVal.Uint() != curVal.Uint() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Float32, reflect.Float64:
		if newVal.Float() != curVal.Float() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Complex64, reflect.Complex128:
		if newVal.Complex() != curVal.Complex() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.String:
		if flags.CaseInsensitive() {
			if !strings.EqualFold(newVal.String(), curVal.String()) {
				vv.checkFlags(flags, namespace, fieldname)
			}
		} else {
			if newVal.String() != curVal.String() {
				vv.checkFlags(flags, namespace, fieldname)
			}
		}

	case reflect.Slice:
		// We already know that newVal is not nil.
		if curVal.IsNil() {
			vv.checkFlags(flags, namespace, fieldname)
			return
		}

		fallthrough

	case reflect.Array:
		if newVal.Len() != curVal.Len() {
			vv.checkFlags(flags, namespace, fieldname)
		} else {
			for i := 0; i < min(newVal.Len(), curVal.Len()); i++ {
				subscript := fmt.Sprintf("[%d]", i)
				vv.recurse(newVal.Index(i), curVal.Index(i), mapKey, namespace, fieldname+subscript, flags)
			}
		}

	case reflect.Interface, reflect.Pointer:
		// We already know that newVal is not nil.
		if curVal.IsNil() {
			vv.checkFlags(flags, namespace, fieldname)
		} else {
			vv.recurse(newVal.Elem(), curVal.Elem(), mapKey, namespace, fieldname, flags)
		}

	case reflect.Map:
		// Determine if newVal and curVal share identical keys.
		var keysEqual = true

		// We already know that newVal is not nil.
		if curVal.IsNil() || newVal.Len() != curVal.Len() {
			keysEqual = false
		} else {
			iter := newVal.MapRange()
			for iter.Next() {
				if !curVal.MapIndex(iter.Key()).IsValid() {
					keysEqual = false
					break
				}
			}
		}

		// Skip recursion if visibility check on the map itself fails.
		if !keysEqual && !vv.checkFlags(flags, namespace, fieldname) {
			return
		}

		// Initialize a zero value for when curVal is missing a key in newVal.
		// If the map value type is a pointer, create a zero value for the type
		// being pointed to.
		var zeroVal reflect.Value
		mapValueType := newVal.Type().Elem()
		if mapValueType.Kind() == reflect.Ptr {
			// This returns a pointer to the new value.
			zeroVal = reflect.New(mapValueType.Elem())
		} else {
			// Follow the pointer to the new value.
			zeroVal = reflect.New(mapValueType).Elem()
		}

		iter := newVal.MapRange()
		for iter.Next() {
			k := iter.Key()
			subscript := fmt.Sprintf("[%q]", k.Interface())
			if curVal.IsNil() || !curVal.MapIndex(k).IsValid() {
				vv.recurse(newVal.MapIndex(k), zeroVal, mapKey, namespace, fieldname+subscript, flags)
			} else {
				vv.recurse(newVal.MapIndex(k), curVal.MapIndex(k), mapKey, namespace, fieldname+subscript, flags)
			}
		}

	case reflect.Struct:
		for i := 0; i < newVal.NumField(); i++ {
			structField := newVal.Type().Field(i)
			mapKeyNext := join(mapKey, structField.Name)
			namespaceNext := join(namespace, fieldname)
			fieldnameNext := GetJSONTagName(vv.structTagMap[mapKeyNext])
			if fieldnameNext == "" {
				fieldnameNext = structField.Name
			}
			vv.recurse(newVal.Field(i), curVal.Field(i), mapKeyNext, namespaceNext, fieldnameNext, flags)
		}
	}
}

func (vv *validateVisibility) checkFlags(flags VisibilityFlags, namespace, fieldname string) bool {
	if flags.ReadOnly() {
		vv.errs = append(vv.errs,
			arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf("Field '%s' is read-only", fieldname),
				Target:  join(namespace, fieldname),
			})
		return false
	} else if vv.updating && !flags.CanUpdate() {
		vv.errs = append(vv.errs,
			arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf("Field '%s' cannot be updated", fieldname),
				Target:  join(namespace, fieldname),
			})
		return false
	}
	return true
}
