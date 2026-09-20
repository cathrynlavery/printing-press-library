//go:build !darwin || !cgo

// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
)

func setKeychainSecret(context.Context, string, string, string) error {
	return errors.New("named service accounts require macOS Keychain with cgo enabled; use --op-service-account-token-env")
}

func getKeychainSecret(context.Context, string, string) (string, error) {
	return "", errors.New("named service accounts require macOS Keychain with cgo enabled")
}

func deleteKeychainSecret(context.Context, string, string) error {
	return errors.New("named service accounts require macOS Keychain with cgo enabled")
}

func repairKeychainAccess(context.Context, string, string) error {
	return errors.New("named service accounts require macOS Keychain with cgo enabled")
}
