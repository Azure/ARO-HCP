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

	// VisibilityNullable is a computed flag that is set when the
	// field has a pointer type and has the VisibilityUpdate flag.
	// It means the field can be set to "null" in a PATCH request.
	VisibilityNullable

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

func (f VisibilityFlags) IsNullable() bool {
	return f&VisibilityNullable != 0
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
	if f&VisibilityNullable != 0 {
		s = append(s, "nullable")
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

// VisibilityMap maps dot-separated struct field names to visibility flags.
type VisibilityMap map[string]VisibilityFlags

func buildVisibilityMap(visibilityMap VisibilityMap, t reflect.Type, path string, implicitFlags VisibilityFlags) {
	switch t.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		flags, found := visibilityMap[path]
		if found && flags.CanUpdate() {
			visibilityMap[path] |= VisibilityNullable
		}
		buildVisibilityMap(visibilityMap, t.Elem(), path, implicitFlags)

	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			var subpath string

			field := t.Field(i)

			if !field.IsExported() {
				continue
			}

			flags := implicitFlags

			// Omit embedded field names from the key.
			// Generated API structs do not have them.
			if field.Anonymous {
				subpath = path
			} else {
				subpath = join(path, field.Name)

				explicitFlags, found := GetVisibilityFlags(field.Tag)
				if found {
					flags = explicitFlags
				}
				visibilityMap[subpath] = flags
			}

			buildVisibilityMap(visibilityMap, field.Type, subpath, flags)
		}
	}
}

// NewVisibilityMap returns a mapping of dot-separated struct field names
// to visibility flags for the given type. Each versioned API should create
// its own visibility map for resource types with PUT and PATCH methods.
//
// Note: This assumes field names for internal and versioned structs are
// identical where visibility is explicitly specified. If some divergence
// emerges, one workaround could be to pass a field name override map.
func NewVisibilityMap[T any]() VisibilityMap {
	visibilityMap := VisibilityMap{}
	buildVisibilityMap(visibilityMap, reflect.TypeFor[T](), "", VisibilityDefault)
	return visibilityMap
}

type validateVisibility struct {
	visibilityMap VisibilityMap
	structTagMap  StructTagMap
	updating      bool
	errs          []arm.CloudErrorBody
}

// ValidateVisibility compares the new value (newVal) to the current value
// (curVal) and returns any violations of visibility restrictions as defined
// by visibilityMap.
func ValidateVisibility(newVal, curVal interface{}, visibilityMap VisibilityMap, structTagMap StructTagMap, updating bool) []arm.CloudErrorBody {
	vv := validateVisibility{
		visibilityMap: visibilityMap,
		structTagMap:  structTagMap,
		updating:      updating,
	}
	vv.recurse(reflect.ValueOf(newVal), reflect.ValueOf(curVal), "", "", "")
	return vv.errs
}

// mapKey is a lookup key for VisibilityMap and StructTagMap.  It DOES NOT
// include subscripts for arrays, maps or slices since all elements are the
// same type.
//
// namespace is the struct field path up to but not including the field being
// evaluated, analogous to path.Dir.  It DOES include subscripts for arrays,
// maps and slices since its purpose is for error reporting.
//
// fieldname is the current field being evaluated, analgous to path.Base.  It
// also includes subscripts for arrays, maps and slices when evaluating their
// immediate elements.
func (vv *validateVisibility) recurse(newVal, curVal reflect.Value, mapKey, namespace, fieldname string) {
	flags := vv.visibilityMap[mapKey]

	if newVal.Type() != curVal.Type() {
		panic(fmt.Sprintf("%s: value types differ (%s vs %s)", join(namespace, fieldname), newVal.Type().Name(), curVal.Type().Name()))
	}

	switch newVal.Kind() {
	case reflect.Bool:
		if newVal.Bool() != curVal.Bool() {
			vv.checkFlags(flags, namespace, fieldname, false)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if newVal.Int() != curVal.Int() {
			vv.checkFlags(flags, namespace, fieldname, false)
		}

	case reflect.Uint, reflect.Uintptr, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if newVal.Uint() != curVal.Uint() {
			vv.checkFlags(flags, namespace, fieldname, false)
		}

	case reflect.Float32, reflect.Float64:
		if newVal.Float() != curVal.Float() {
			vv.checkFlags(flags, namespace, fieldname, false)
		}

	case reflect.Complex64, reflect.Complex128:
		if newVal.Complex() != curVal.Complex() {
			vv.checkFlags(flags, namespace, fieldname, false)
		}

	case reflect.String:
		if flags.CaseInsensitive() {
			if !strings.EqualFold(newVal.String(), curVal.String()) {
				vv.checkFlags(flags, namespace, fieldname, false)
			}
		} else {
			if newVal.String() != curVal.String() {
				vv.checkFlags(flags, namespace, fieldname, false)
			}
		}

	case reflect.Slice:
		// Treat a nil slice and an empty slice as equal.
		newValIsNil := (newVal.IsNil() || newVal.Len() == 0)
		curValIsNil := (curVal.IsNil() || curVal.Len() == 0)

		if newValIsNil != curValIsNil {
			vv.checkFlags(flags, namespace, fieldname, newValIsNil)
			return
		}

		fallthrough

	case reflect.Array:
		if newVal.Len() != curVal.Len() {
			vv.checkFlags(flags, namespace, fieldname, false)
		} else {
			for i := 0; i < min(newVal.Len(), curVal.Len()); i++ {
				subscript := fmt.Sprintf("[%d]", i)
				vv.recurse(newVal.Index(i), curVal.Index(i), mapKey, namespace, fieldname+subscript)
			}
		}

	case reflect.Interface, reflect.Pointer:
		var newValIsNil, curValIsNil bool

		// If the field is NOT nullable, treat a nil pointer and a
		// pointer to the zero value of the pointer's type as equal.
		if flags.IsNullable() {
			newValIsNil = newVal.IsNil()
			curValIsNil = newVal.IsNil()
		} else {
			newValIsNil = (newVal.IsNil() || newVal.Elem().IsZero())
			curValIsNil = (curVal.IsNil() || curVal.Elem().IsZero())
		}

		if newValIsNil != curValIsNil {
			if !vv.checkFlags(flags, namespace, fieldname, newVal.IsNil()) {
				return
			}
		}
		if !newVal.IsNil() && !curVal.IsNil() {
			vv.recurse(newVal.Elem(), curVal.Elem(), mapKey, namespace, fieldname)
		}

	case reflect.Map:
		// Determine if newVal and curVal share identical keys.
		var keysEqual = true

		// Treat a nil map and an empty map as equal.
		newValIsNil := (newVal.IsNil() || newVal.Len() == 0)
		curValIsNil := (curVal.IsNil() || curVal.Len() == 0)

		if newValIsNil != curValIsNil || newVal.Len() != curVal.Len() {
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
		if !keysEqual && !vv.checkFlags(flags, namespace, fieldname, newValIsNil) {
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
				vv.recurse(newVal.MapIndex(k), zeroVal, mapKey, namespace, fieldname+subscript)
			} else {
				vv.recurse(newVal.MapIndex(k), curVal.MapIndex(k), mapKey, namespace, fieldname+subscript)
			}
		}

	case reflect.Struct:
		for i := 0; i < newVal.NumField(); i++ {
			structField := newVal.Type().Field(i)

			if structField.Anonymous {
				vv.recurse(newVal.Field(i), curVal.Field(i), mapKey, namespace, fieldname)
			} else {
				mapKeyNext := join(mapKey, structField.Name)
				namespaceNext := join(namespace, fieldname)
				fieldnameNext := GetJSONTagName(vv.structTagMap[mapKeyNext])
				if fieldnameNext == "" {
					fieldnameNext = structField.Name
				}
				vv.recurse(newVal.Field(i), curVal.Field(i), mapKeyNext, namespaceNext, fieldnameNext)
			}
		}
	}
}

func (vv *validateVisibility) checkFlags(flags VisibilityFlags, namespace, fieldname string, newValIsNil bool) bool {
	var message string

	if vv.updating && newValIsNil && !flags.IsNullable() {
		message = fmt.Sprintf("Field '%s' cannot be removed", fieldname)
	} else if vv.updating && !flags.CanUpdate() {
		message = fmt.Sprintf("Field '%s' cannot be updated", fieldname)
	} else if flags.ReadOnly() {
		message = fmt.Sprintf("Field '%s' is read-only", fieldname)
	}

	if message != "" {
		vv.errs = append(vv.errs,
			arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: message,
				Target:  join(namespace, fieldname),
			})
		return false
	}

	return true
}
