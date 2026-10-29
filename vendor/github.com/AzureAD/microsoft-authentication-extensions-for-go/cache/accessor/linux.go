// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See LICENSE in the project root for license information.

//go:build linux
// +build linux

package accessor

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef struct
{
    int domain;
    int code;
    char *message;
} gError;

typedef struct
{
    const char *name;
    int type;
} schemaAttribute;

typedef struct
{
    char *name;
    int flags;
    schemaAttribute attributes[32];

    // private fields
    int r;
    char *r2;
    char *r3;
    char *r4;
    char *r5;
    char *r6;
    char *r7;
    char *r8;
} schema;

schema *new_schema(char *name, const char *key1, const char *key2)
{
    int i = 0;
    schema *s;
    s = malloc(sizeof(schema));
    s->flags = 0; // SECRET_SCHEMA_NONE
    s->name = name;
    if (key1 != NULL)
    {
        s->attributes[i++] = (schemaAttribute){key1, 0}; // 0 == SECRET_SCHEMA_ATTRIBUTE_STRING
    }
    if (key2 != NULL)
    {
        s->attributes[i++] = (schemaAttribute){key2, 0};
    }
    s->attributes[i] = (schemaAttribute){NULL, 0};
    return s;
}

// free a gError. f must be a pointer to g_error_free
void free_g_error(void *f, gError *err)
{
    void (*fn)(gError *err);
    fn = (void (*)(gError *err))f;
    fn(err);
}

// clear (delete) a secret. f must be a pointer to secret_password_clear_sync
// https://gnome.pages.gitlab.gnome.org/libsecret/func.password_clear_sync.html
int clear(void *f, schema *sch, void* cancellable, gError **err, char *key1, char *value1, char *key2, char *value2)
{
    int (*fn)(schema *sch, void* cancellable, gError **err, char *key1, char *value1, char *key2, char *value2, ...);
    fn = (int (*)(schema *sch, void* cancellable, gError **err, char *key1, char *value1, char *key2, char *value2, ...))f;
    int r = fn(sch, cancellable, err, key1, value1, key2, value2, NULL);
    return r;
}

// lookup a password. f must be a pointer to secret_password_lookup_sync
// https://gnome.pages.gitlab.gnome.org/libsecret/func.password_lookup_sync.html
char *lookup(void *f, schema *sch, void* cancellable, gError **err, char *key1, char *value1, char *key2, char *value2)
{
    char *(*fn)(schema *s, void *cancellable, gError **err, char *attrKey1, char *attrValue1, char *attrKey2, char* attrValue2, ...);
    fn = (char *(*)(schema *s, void *cancellable, gError **err, char *attrKey1, char *attrValue1, char *attrKey2, char* attrValue2, ...))f;
    char *r = fn(sch, cancellable, err, key1, value1, key2, value2, NULL);
    return r;
}

// store a password. f must be a pointer to secret_password_store_sync
// https://gnome.pages.gitlab.gnome.org/libsecret/func.password_store_sync.html
int store(void *f, schema *sch, char* collection, char *label, char *password, void* cancellable, gError **err, char *key1, char *value1, char *key2, char *value2)
{
    int (*fn)(schema *s, char *collection, char *label, char *data, void *cancellable, gError **err, ...);
    fn = (int (*)(schema *s, char *collection, char *label, char *data, void *cancellable, gError **err, ...))f;
    int r = fn(sch, collection, label, password, cancellable, err, key1, value1, key2, value2, NULL);
    return r;
}
*/
import "C"
import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

const so = "libsecret-1.so"

type attribute struct {
	name, value string
}

type option func(*Storage) error

// WithAttribute adds an attribute to the schema representing the cache.
// [Storage] supports up to 2 attributes.
func WithAttribute(name, value string) option {
	return func(s *Storage) error {
		if len(s.attributes) == 2 {
			return errors.New("Storage supports up to 2 attributes")
		}
		s.attributes = append(s.attributes, attribute{name: name, value: value})
		return nil
	}
}

// WithLabel sets a label on the schema representing the cache. The default label is "MSALCache".
func WithLabel(label string) option {
	return func(s *Storage) error {
		s.label = label
		return nil
	}
}

// Storage uses libsecret to store data with a DBus Secret Service such as GNOME Keyring or KDE Wallet. The Service
// must be unlocked before Storage can access it. Unlocking typically requires user interaction, and some systems may
// be unable to unlock the Service in a headless environment such as an SSH session.
type Storage struct {
	// attributes are key/value pairs on the secret schema
	attributes []attribute
	// handle is an opaque handle for libsecret returned by dlopen(). It should be
	// released via dlclose() when no longer needed so the loader knows when it's
	// safe to unload libsecret.
	handle unsafe.Pointer
	// label of the secret schema
	label string
	// clear, freeError, lookup and store are the addresses of libsecret functions
	clear, freeError, lookup, store unsafe.Pointer
	// schema identifies the cached data in the secret service
	schema *C.schema
}

// New is the constructor for Storage. "name" is the name of the secret schema.
func New(name string, opts ...option) (*Storage, error) {
	s := Storage{label: "MSALCache"}
	for _, o := range opts {
		if err := o(&s); err != nil {
			return nil, err
		}
	}
	n := C.CString(so)
	defer C.free(unsafe.Pointer(n))

	// set the handle and finalizer first so any handle will be
	// released even when this constructor goes on to return an error
	s.handle = C.dlopen(n, C.RTLD_LAZY)
	if s.handle == nil {
		msg := fmt.Sprintf("encrypted storage isn't possible because the dynamic linker couldn't open %s", so)
		if e := C.dlerror(); e != nil {
			msg += fmt.Sprintf(". The underlying error is %q", C.GoString(e))
		}
		return nil, errors.New(msg)
	}
	runtime.SetFinalizer(&s, func(s *Storage) {
		if s.handle != nil {
			C.dlclose(s.handle)
		}
		if s.schema != nil {
			for _, attr := range s.schema.attributes {
				if attr.name != nil {
					C.free(unsafe.Pointer(attr.name))
				}
			}
			C.free(unsafe.Pointer(s.schema.name))
			C.free(unsafe.Pointer(s.schema))
		}
	})

	clear, err := s.symbol("secret_password_clear_sync")
	if err != nil {
		return nil, err
	}
	freeError, err := s.symbol("g_error_free")
	if err != nil {
		return nil, err
	}
	lookup, err := s.symbol("secret_password_lookup_sync")
	if err != nil {
		return nil, err
	}
	store, err := s.symbol("secret_password_store_sync")
	if err != nil {
		return nil, err
	}
	s.clear = clear
	s.freeError = freeError
	s.lookup = lookup
	s.store = store

	// the first nil terminates the list and libsecret ignores any extras
	attrs := []*C.char{nil, nil}
	for i, attr := range s.attributes {
		// libsecret hangs on to these pointers; the finalizer frees them
		attrs[i] = C.CString(attr.name)
	}
	s.schema = C.new_schema(C.CString(name), attrs[0], attrs[1])
	return &s, nil
}

// Delete deletes the stored data, if any exists.
func (s *Storage) Delete(context.Context) error {
	// the first nil terminates the list and libsecret ignores any extras
	attrs := []*C.char{nil, nil, nil, nil}
	for i, attr := range s.attributes {
		name := C.CString(attr.name)
		defer C.free(unsafe.Pointer(name))
		value := C.CString(attr.value)
		defer C.free(unsafe.Pointer(value))
		attrs[i*2] = name
		attrs[(i*2)+1] = value
	}
	var e *C.gError
	_ = C.clear(s.clear, s.schema, nil, &e, attrs[0], attrs[1], attrs[2], attrs[3])
	if e != nil {
		defer C.free_g_error(s.freeError, e)
		return fmt.Errorf("couldn't delete cache data: %q", C.GoString(e.message))
	}
	return nil
}

// Read returns data stored according to the secret schema or, if no such data exists, a nil slice and nil error.
func (s *Storage) Read(context.Context) ([]byte, error) {
	// the first nil terminates the list and libsecret ignores any extras
	attrs := []*C.char{nil, nil, nil, nil}
	for i, attr := range s.attributes {
		name := C.CString(attr.name)
		defer C.free(unsafe.Pointer(name))
		value := C.CString(attr.value)
		defer C.free(unsafe.Pointer(value))
		attrs[i*2] = name
		attrs[(i*2)+1] = value
	}
	var e *C.gError
	data := C.lookup(s.lookup, s.schema, nil, &e, attrs[0], attrs[1], attrs[2], attrs[3])
	if e != nil {
		defer C.free_g_error(s.freeError, e)
		return nil, fmt.Errorf("couldn't read data from secret service: %q", C.GoString(e.message))
	}
	if data == nil {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(data))
	result, err := base64.StdEncoding.DecodeString(C.GoString(data))
	return result, err
}

// Write stores cache data.
func (s *Storage) Write(_ context.Context, data []byte) error {
	// the first nil terminates the list and libsecret ignores any extras
	attrs := []*C.char{nil, nil, nil, nil}
	for i, attr := range s.attributes {
		name := C.CString(attr.name)
		defer C.free(unsafe.Pointer(name))
		value := C.CString(attr.value)
		defer C.free(unsafe.Pointer(value))
		attrs[i*2] = name
		attrs[(i*2)+1] = value
	}
	pw := C.CString(base64.StdEncoding.EncodeToString(data))
	defer C.free(unsafe.Pointer(pw))
	var label *C.char
	if s.label != "" {
		label = C.CString(s.label)
		defer C.free(unsafe.Pointer(label))
	}
	var e *C.gError
	if r := C.store(s.store, s.schema, nil, label, pw, nil, &e, attrs[0], attrs[1], attrs[2], attrs[3]); r == 0 {
		msg := "couldn't write data to secret service"
		if e != nil {
			defer C.free_g_error(s.freeError, e)
			if e.message != nil {
				msg += ": " + C.GoString(e.message)
			}
		}
		return errors.New(msg)
	}
	return nil
}

func (s *Storage) symbol(name string) (unsafe.Pointer, error) {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	C.dlerror()
	fp := C.dlsym(s.handle, n)
	if e := C.dlerror(); e != nil {
		return nil, fmt.Errorf("couldn't load %q: %s", name, C.GoString(e))
	}
	return fp, nil
}

var _ Accessor = (*Storage)(nil)
