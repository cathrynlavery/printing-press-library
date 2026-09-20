//go:build !darwin && !linux

// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"os"
)

func tryLockServiceAccountFile(_ *os.File) (bool, error) {
	return false, errors.New("named service accounts require macOS Keychain; use --op-service-account-token-env on this platform")
}
