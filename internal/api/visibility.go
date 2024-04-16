package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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

func buildStructTagMap(m StructTagMap, t reflect.Type, path string) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		buildStructTagMap(m, t.Elem(), path)

	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			subpath := join(path, field.Name)

			if len(field.Tag) > 0 {
				m[subpath] = field.Tag
			}

			buildStructTagMap(m, field.Type, subpath)
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
	m := StructTagMap{}
	buildStructTagMap(m, reflect.TypeFor[T](), "")
	return m
}

type validateVisibility struct {
	m        StructTagMap
	updating bool
	errs     []arm.CloudErrorBody
}

func ValidateVisibility(v, w interface{}, m StructTagMap, updating bool) []arm.CloudErrorBody {
	vv := validateVisibility{
		m:        m,
		updating: updating,
	}
	vv.recurse(reflect.ValueOf(v), reflect.ValueOf(w), "", "", "", VisibilityDefault)
	return vv.errs
}

func (vv *validateVisibility) recurse(v, w reflect.Value, mKey, namespace, fieldname string, implicitVisibility VisibilityFlags) {
	flags, ok := GetVisibilityFlags(vv.m[mKey])
	if !ok {
		flags = implicitVisibility
	}

	if v.Type() != w.Type() {
		panic(fmt.Sprintf("%s: value types differ (%s vs %s)", join(namespace, fieldname), v.Type().Name(), w.Type().Name()))
	}

	// Generated API structs are all pointer fields. A nil value in
	// the incoming request (v) means the value is absent, which is
	// always acceptable for visibility validation.
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return
		}
	}

	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() != w.Bool() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Int() != w.Int() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Uint, reflect.Uintptr, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() != w.Uint() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Float32, reflect.Float64:
		if v.Float() != w.Float() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.Complex64, reflect.Complex128:
		if v.Complex() != w.Complex() {
			vv.checkFlags(flags, namespace, fieldname)
		}

	case reflect.String:
		if flags.CaseInsensitive() {
			if !strings.EqualFold(v.String(), w.String()) {
				vv.checkFlags(flags, namespace, fieldname)
			}
		} else {
			if v.String() != w.String() {
				vv.checkFlags(flags, namespace, fieldname)
			}
		}

	case reflect.Slice:
		if w.IsNil() {
			vv.checkFlags(flags, namespace, fieldname)
			return
		}

		fallthrough

	case reflect.Array:
		if v.Len() != w.Len() {
			vv.checkFlags(flags, namespace, fieldname)
		} else {
			for i := 0; i < min(v.Len(), w.Len()); i++ {
				subscript := fmt.Sprintf("[%d]", i)
				vv.recurse(v.Index(i), w.Index(i), mKey, namespace, fieldname+subscript, flags)
			}
		}

	case reflect.Interface, reflect.Pointer:
		if w.IsNil() {
			vv.checkFlags(flags, namespace, fieldname)
		} else {
			vv.recurse(v.Elem(), w.Elem(), mKey, namespace, fieldname, flags)
		}

	case reflect.Map:
		if w.IsNil() || v.Len() != w.Len() {
			vv.checkFlags(flags, namespace, fieldname)
		} else {
			iter := v.MapRange()
			for iter.Next() {
				k := iter.Key()

				subscript := fmt.Sprintf("[%q]", k.Interface())
				if w.MapIndex(k).IsValid() {
					vv.recurse(v.MapIndex(k), w.MapIndex(k), mKey, namespace, fieldname+subscript, flags)
				} else {
					vv.checkFlags(flags, namespace, fieldname+subscript)
				}
			}
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			structField := v.Type().Field(i)
			mKeyNext := join(mKey, structField.Name)
			namespaceNext := join(namespace, fieldname)
			fieldnameNext := GetJSONTagName(vv.m[mKeyNext])
			if fieldnameNext == "" {
				fieldnameNext = structField.Name
			}
			vv.recurse(v.Field(i), w.Field(i), mKeyNext, namespaceNext, fieldnameNext, flags)
		}
	}
}

func (vv *validateVisibility) checkFlags(flags VisibilityFlags, namespace, fieldname string) {
	if flags.ReadOnly() {
		vv.errs = append(vv.errs,
			arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf("Field '%s' is read-only", fieldname),
				Target:  join(namespace, fieldname),
			})
	} else if vv.updating && !flags.CanUpdate() {
		vv.errs = append(vv.errs,
			arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf("Field '%s' cannot be updated", fieldname),
				Target:  join(namespace, fieldname),
			})
	}
}
