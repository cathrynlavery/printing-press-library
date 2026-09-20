//go:build darwin || linux

// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

type blockingSecretStore struct {
	inner   secretStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingSecretStore) pause() {
	first := false
	s.once.Do(func() {
		first = true
		close(s.entered)
	})
	if first {
		<-s.release
	}
}

func (s *blockingSecretStore) Get(ctx context.Context, name string) (string, error) {
	token, err := s.inner.Get(ctx, name)
	s.pause()
	return token, err
}

func (s *blockingSecretStore) Set(ctx context.Context, name, token string) error {
	err := s.inner.Set(ctx, name, token)
	s.pause()
	return err
}

func (s *blockingSecretStore) Delete(ctx context.Context, name string) error {
	err := s.inner.Delete(ctx, name)
	s.pause()
	return err
}

func serviceAccountTestCommand(operation, name string) *cobra.Command {
	flags := &rootFlags{noInput: true, yes: true, asJSON: true}
	var cmd *cobra.Command
	switch operation {
	case "add":
		cmd = newServiceAccountAddCmd(flags)
		cmd.SetArgs([]string{name, "--account", "example.com", "--token-stdin"})
		cmd.SetIn(strings.NewReader("transaction-unit-secret"))
	case "remove":
		cmd = newServiceAccountRemoveCmd(flags)
		cmd.SetArgs([]string{name})
	case "doctor":
		cmd = newServiceAccountDoctorCmd(flags)
		cmd.SetArgs([]string{name})
	}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}

func TestServiceAccountTransactionsSerializeWritersAndTokenReaders(t *testing.T) {
	for _, operation := range []string{"add", "remove", "doctor"} {
		t.Run(operation, func(t *testing.T) {
			fake := withFakeServiceAccountStore(t)
			t.Setenv("PRINTING_PRESS_VERIFY", "1")
			t.Setenv("OP_CONNECT_HOST", "")
			t.Setenv("OP_CONNECT_TOKEN", "")
			if operation != "add" {
				fake.values["team"] = "existing-unit-secret"
				saveTestServiceAccount(t, "team", "example.com")
			}
			blocking := &blockingSecretStore{inner: fake, entered: make(chan struct{}), release: make(chan struct{})}
			serviceAccountSecrets = blocking
			cmd := serviceAccountTestCommand(operation, "team")
			finished := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				finished <- cmd.ExecuteContext(context.Background())
				close(done)
			}()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(blocking.release) }) }
			defer func() { release(); <-done }()
			select {
			case <-blocking.entered:
			case err := <-finished:
				t.Fatalf("transaction exited before Keychain access: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("transaction did not reach Keychain access")
			}

			// A second writer must not touch Keychain or load a stale snapshot while
			// the first transaction is between secure storage and metadata commit.
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err := serviceAccountTestCommand("add", "other").ExecuteContext(ctx)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("concurrent writer did not wait for complete transaction: %v", err)
			}
			if _, exists := fake.values["other"]; exists {
				t.Fatal("blocked writer changed secure storage")
			}
			ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, err = resolveOpAuth(ctx, &rootFlags{opServiceAccount: "team"})
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("named reader observed an unfinished transaction: %v", err)
			}
			release()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if err := serviceAccountTestCommand("add", "other").Execute(); err != nil {
				t.Fatalf("writer could not proceed after commit: %v", err)
			}
			store, err := loadServiceAccountStore()
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := store.ServiceAccounts["other"]; !exists {
				t.Fatal("new account metadata disappeared")
			}
			_, teamExists := store.ServiceAccounts["team"]
			if teamExists != (operation != "remove") {
				t.Fatal("first transaction was overwritten")
			}
			if len(store.ServiceAccounts) != len(fake.values) {
				t.Fatal("Keychain and metadata diverged")
			}
			if operation == "doctor" && store.ServiceAccounts["team"].LastVerified == nil {
				t.Fatal("doctor verification metadata was lost")
			}
		})
	}
}

func TestServiceAccountLockAcrossProcesses(t *testing.T) {
	withFakeServiceAccountStore(t)
	unlock, err := lockServiceAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServiceAccountLockSubprocess$")
	cmd.Env = append(os.Environ(), "PP_TEST_ACCOUNT_LOCK_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("another process bypassed the lock: %v\n%s", err, out)
	}
}

func TestServiceAccountLockSubprocess(t *testing.T) {
	if os.Getenv("PP_TEST_ACCOUNT_LOCK_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	unlock, err := lockServiceAccounts(ctx)
	if unlock != nil {
		unlock()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected another process's lock to block until deadline: %v", err)
	}
}

func TestServiceAccountLockHonorsCancellation(t *testing.T) {
	withFakeServiceAccountStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unlock, err := lockServiceAccounts(ctx)
	if unlock != nil {
		unlock()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled operation acquired lock: %v", err)
	}
}

func TestServiceAccountDoctorTimeoutReleasesTransaction(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "doctor-unit-secret"
	saveTestServiceAccount(t, "team", "example.com")
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "op"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRINTING_PRESS_VERIFY", "")
	cmd := newServiceAccountDoctorCmd(&rootFlags{timeout: 100 * time.Millisecond})
	cmd.SetArgs([]string{"team"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	started := time.Now()
	if err := cmd.Execute(); err == nil {
		t.Fatal("hung doctor unexpectedly succeeded")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("doctor ignored its subprocess timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	unlock, err := lockServiceAccounts(ctx)
	if err != nil {
		t.Fatalf("doctor retained lock after timeout: %v", err)
	}
	unlock()
	meta, _, err := getServiceAccountMetadata("team")
	if err != nil || meta.LastVerified != nil {
		t.Fatal("timed-out doctor marked account verified")
	}
}

func TestServiceAccountPersistentLockFileCanBeReused(t *testing.T) {
	withFakeServiceAccountStore(t)
	for range 2 {
		unlock, err := lockServiceAccounts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
}

type rollbackBlockingSecretStore struct {
	*fakeSecretStore
	entered chan struct{}
	release chan struct{}
}

func (s *rollbackBlockingSecretStore) Set(ctx context.Context, name, token string) error {
	if s.setCalls == 1 {
		close(s.entered)
		<-s.release
	}
	return s.fakeSecretStore.Set(ctx, name, token)
}

func TestServiceAccountRollbackRetainsTransactionLock(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "original-unit-secret"
	saveTestServiceAccount(t, "team", "example.com")
	p, err := serviceAccountStorePath()
	if err != nil {
		t.Fatal(err)
	}
	fake.afterMutation = func() {
		if err := os.Remove(p); err != nil {
			panic(err)
		}
		if err := os.Mkdir(p, 0o700); err != nil {
			panic(err)
		}
	}
	blocking := &rollbackBlockingSecretStore{fakeSecretStore: fake, entered: make(chan struct{}), release: make(chan struct{})}
	serviceAccountSecrets = blocking
	finished := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		finished <- serviceAccountTestCommand("add", "team").Execute()
		close(done)
	}()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocking.release) }) }
	defer func() { release(); <-done }()
	select {
	case <-blocking.entered:
	case err := <-finished:
		t.Fatalf("did not reach rollback: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("rollback did not begin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = resolveOpAuth(ctx, &rootFlags{opServiceAccount: "team"})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reader entered unfinished rollback: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = serviceAccountTestCommand("add", "other").ExecuteContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer entered unfinished rollback: %v", err)
	}
	release()
	if err := <-finished; err == nil || !strings.Contains(err.Error(), "persisting service-account metadata") {
		t.Fatalf("metadata failure was not preserved: %v", err)
	}
	if fake.values["team"] != "original-unit-secret" || len(fake.values) != 1 {
		t.Fatal("rollback failed to restore secure storage")
	}
}
