// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The lock covers metadata snapshots, Keychain operations, commits, and rollbacks.
// Keep the lock file in place: unlinking it could let waiters lock different inodes.
func lockServiceAccounts(ctx context.Context) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("waiting for service-account transaction: %w", err)
	}
	p, err := serviceAccountStorePath()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening service-account transaction lock: %w", err)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, fmt.Errorf("waiting for service-account transaction: %w", err)
		}
		locked, err := tryLockServiceAccountFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("locking service-account transaction: %w", err)
		}
		if locked {
			// Closing the descriptor releases the kernel lock, including on process exit.
			return func() { f.Close() }, nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
