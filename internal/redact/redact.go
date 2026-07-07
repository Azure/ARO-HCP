// Copyright (c) 2021 Sam Kreter
//
// This file is derived from github.com/Azure/redact and modified in-repo.
// It is licensed under the MIT License; see internal/redact/LICENSE.

package redact

import (
	"errors"
	"reflect"
)

const (
	tagName        = "redact"
	nonSecret      = "nonsecret"
	noTraverse     = "notraverse"
	RedactStrConst = "REDACTED"
)

type redactor func(string) string

var redactors = map[string]redactor{}

// AddRedactor allows for adding custom functionality based on tag values.
func AddRedactor(key string, r redactor) {
	redactors[key] = r
}

// Redact redacts all strings without the nonsecret tag.
func Redact(iface any) error {
	ifv := reflect.ValueOf(iface)
	if ifv.Kind() != reflect.Ptr {
		return errors.New("not a pointer")
	}

	redact(reflect.Indirect(ifv), nonSecret)
	return nil
}

func redact(v reflect.Value, tag string) {
	switch v.Kind() {
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			redact(v.Index(i), tag)
		}

	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			redact(v.Elem(), tag)
		}

	case reflect.Map:
		if !v.IsNil() {
			for _, key := range v.MapKeys() {
				val := reflect.New(v.Type().Elem()).Elem()
				val.Set(v.MapIndex(key))
				redact(val, tag)
				v.SetMapIndex(key, val)
			}
		}

	case reflect.Slice:
		if !v.IsNil() {
			for i := 0; i < v.Len(); i++ {
				redact(v.Index(i), tag)
			}
		}

	case reflect.String:
		if v.CanSet() {
			v.SetString(transformString(v.String(), tag))
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanSet() {
				continue
			}

			tagValue, _ := v.Type().Field(i).Tag.Lookup(tagName)
			if tagValue == noTraverse {
				continue
			}
			redact(field, tagValue)
		}
	}
}

func transformString(input, tagVal string) string {
	switch tagVal {
	case nonSecret:
		return input
	default:
		customRedactor, ok := redactors[tagVal]
		if !ok {
			return RedactStrConst
		}
		return customRedactor(input)
	}
}
