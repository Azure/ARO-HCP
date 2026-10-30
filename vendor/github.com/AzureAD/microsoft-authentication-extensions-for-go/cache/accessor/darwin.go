// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See LICENSE in the project root for license information.

//go:build darwin && cgo
// +build darwin,cgo

package accessor

import (
	"context"
	"errors"

	"github.com/keybase/go-keychain"
)

type option func(*Storage) error

// WithAccount sets an optional account name for the keychain item holding cached data.
func WithAccount(name string) option {
	return func(s *Storage) error {
		s.account = name
		return nil
	}
}

// Storage stores data as a password on the macOS keychain. The keychain must be unlocked before Storage can read
// or write data. macOS may not allow keychain access from a headless environment such as an SSH session.
type Storage struct {
	account, service string
}

// New is the constructor for Storage. "servName" is the service name for the keychain item holding cached data.
func New(servName string, opts ...option) (*Storage, error) {
	if servName == "" {
		return nil, errors.New("servName can't be empty")
	}
	s := Storage{service: servName}
	for _, o := range opts {
		if err := o(&s); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// Delete deletes the stored data, if any exists.
func (s *Storage) Delete(context.Context) error {
	err := keychain.DeleteGenericPasswordItem(s.service, s.account)
	if errors.Is(err, keychain.ErrorItemNotFound) || errors.Is(err, keychain.ErrorNoSuchKeychain) {
		return nil
	}
	return err
}

// Read returns data stored on the keychain or, if the keychain item doesn't exist, a nil slice and nil error.
func (s *Storage) Read(context.Context) ([]byte, error) {
	data, err := keychain.GetGenericPassword(s.service, s.account, "", "")
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Write stores data on the keychain.
func (s *Storage) Write(_ context.Context, data []byte) error {
	pw, err := keychain.GetGenericPassword(s.service, s.account, "", "")
	if err != nil {
		return err
	}
	item := keychain.NewGenericPassword(s.service, s.account, "", nil, "")
	if pw == nil {
		// password not found: add it to the keychain
		item.SetData(data)
		err = keychain.AddItem(item)
	} else {
		// password found: update its value
		update := keychain.NewGenericPassword(s.service, s.account, "", data, "")
		err = keychain.UpdateItem(item, update)
	}
	return err
}

var _ Accessor = (*Storage)(nil)
