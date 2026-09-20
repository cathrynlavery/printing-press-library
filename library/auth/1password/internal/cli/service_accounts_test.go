// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeSecretStore struct {
	values    map[string]string
	deleted   []string
	getErr    error
	getCalls  int
	setCalls  int
	failSetAt int
}

func (f *fakeSecretStore) Set(_ context.Context, name, value string) error {
	f.setCalls++
	if f.failSetAt == f.setCalls {
		return errors.New("simulated Keychain write failure")
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[name] = value
	return nil
}
func (f *fakeSecretStore) Get(_ context.Context, name string) (string, error) {
	f.getCalls++
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.values[name]
	if !ok {
		return "", errors.New("secret missing")
	}
	return v, nil
}
func (f *fakeSecretStore) Delete(_ context.Context, name string) error {
	if _, ok := f.values[name]; !ok {
		return errors.New("secret missing")
	}
	delete(f.values, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func withFakeServiceAccountStore(t *testing.T) *fakeSecretStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	fake := &fakeSecretStore{values: map[string]string{}}
	previous := serviceAccountSecrets
	serviceAccountSecrets = fake
	t.Cleanup(func() { serviceAccountSecrets = previous })
	return fake
}

func saveTestServiceAccount(t *testing.T, name, account string) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := saveServiceAccountStore(&serviceAccountStoreFile{ServiceAccounts: map[string]serviceAccountMetadata{
		name: {Name: name, Account: account, CreatedAt: now, UpdatedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestNamedSelectionWinsOverAmbientOnlyInChildEnv(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "named-unit-secret"
	saveTestServiceAccount(t, "team", "example-team.1password.com")
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "ambient-unit-secret")

	auth, err := resolveOpAuth(context.Background(), &rootFlags{opServiceAccount: "team"})
	if err != nil {
		t.Fatal(err)
	}
	if auth.mode != "service-account-profile" || auth.tokenSource != "keychain" {
		t.Fatalf("unexpected auth metadata: %#v", auth)
	}
	if got := os.Getenv("OP_SERVICE_ACCOUNT_TOKEN"); got != "ambient-unit-secret" {
		t.Fatal("parent environment was modified")
	}
	child := strings.Join(auth.childEnv(), "\n")
	if !strings.Contains(child, "OP_SERVICE_ACCOUNT_TOKEN=named-unit-secret") || strings.Contains(child, "ambient-unit-secret") {
		t.Fatal("child environment did not replace ambient auth")
	}
}

func TestEnvSelectionAndConnectConflict(t *testing.T) {
	withFakeServiceAccountStore(t)
	t.Setenv("TEAM_OP_TOKEN", "env-unit-secret")
	auth, err := resolveOpAuth(context.Background(), &rootFlags{opServiceAccountTokenEnv: "TEAM_OP_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if auth.mode != "service-account-env" || auth.tokenSource != "env:TEAM_OP_TOKEN" {
		t.Fatalf("unexpected auth: %#v", auth)
	}
	child := strings.Join(auth.childEnv(), "\n")
	if strings.Contains(child, "TEAM_OP_TOKEN=") {
		t.Fatal("source token variable leaked into child environment")
	}
	if !strings.Contains(child, "OP_SERVICE_ACCOUNT_TOKEN=env-unit-secret") {
		t.Fatal("selected token missing from child environment")
	}

	t.Setenv("OP_CONNECT_HOST", "https://connect.invalid")
	if _, err := resolveOpAuth(context.Background(), &rootFlags{opServiceAccountTokenEnv: "TEAM_OP_TOKEN"}); err == nil || !strings.Contains(err.Error(), "OP_CONNECT_HOST") {
		t.Fatalf("expected Connect conflict, got %v", err)
	}
}

func TestMissingNamedSelectionHardFailsAndAmbientRemainsCompatible(t *testing.T) {
	withFakeServiceAccountStore(t)
	if _, err := resolveOpAuth(context.Background(), &rootFlags{opServiceAccount: "missing"}); err == nil {
		t.Fatal("missing named account did not fail")
	}
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "ambient-unit-secret")
	auth, err := resolveOpAuth(context.Background(), &rootFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if auth.mode != "service-account" || auth.tokenSource != "ambient" {
		t.Fatalf("ambient auth changed: %#v", auth)
	}
}

func TestOpRunnerUsesEnvironmentNotArgvAndRedactsOutput(t *testing.T) {
	binDir := t.TempDir()
	opPath := filepath.Join(binDir, "op")
	script := `#!/bin/sh
case " $* " in *" runner-unit-secret "*) echo "token leaked in argv" >&2; exit 31;; esac
[ "$OP_SERVICE_ACCOUNT_TOKEN" = "runner-unit-secret" ] || { echo "selected token missing" >&2; exit 32; }
printf '{"ok":true,"echo":"%s"}\n' "$OP_SERVICE_ACCOUNT_TOKEN"
`
	if err := os.WriteFile(opPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	auth := opAuthContext{mode: "service-account-env", tokenSource: "env:TEST", token: "runner-unit-secret", explicitToken: true}
	ctx := context.WithValue(context.Background(), opAuthContextKey{}, auth)
	out, _, err := newOpRunner().command(ctx, "vault", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "runner-unit-secret") || !strings.Contains(string(out), "[REDACTED]") {
		t.Fatalf("runner output was not redacted: %s", out)
	}
}

func TestServiceAccountListShowDoctorRemoveWithFakeStore(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "doctor-unit-secret"
	saveTestServiceAccount(t, "team", "example-team.1password.com")
	t.Setenv("PRINTING_PRESS_VERIFY", "1")

	for _, args := range [][]string{{"service-accounts", "list", "--agent"}, {"service-accounts", "show", "team", "--agent"}, {"service-accounts", "doctor", "team", "--agent"}} {
		flags := &rootFlags{}
		cmd := newRootCmd(flags)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if strings.Contains(out.String(), "doctor-unit-secret") {
			t.Fatalf("%v revealed token: %s", args, out.String())
		}
	}
	flags := &rootFlags{}
	cmd := newRootCmd(flags)
	cmd.SetArgs([]string{"service-accounts", "remove", "team", "--yes", "--agent"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "team" {
		t.Fatalf("secret not removed: %#v", fake.deleted)
	}
}

func TestRepairAccessRejectsAgentModeWithoutReadingToken(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "repair-unit-secret"
	saveTestServiceAccount(t, "team", "example-team.1password.com")

	flags := &rootFlags{}
	cmd := newRootCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"service-accounts", "repair-access", "team", "--agent"})
	err := cmd.Execute()
	wantError := "interactive macOS session"
	if runtime.GOOS != "darwin" {
		wantError = "available only on macOS"
	}
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("expected interactive repair error, got %v", err)
	}
	if strings.Contains(output.String(), "repair-unit-secret") {
		t.Fatalf("repair error revealed token: %s", output.String())
	}
}

func TestProfilesNeverCaptureOrApplyAuthSelectors(t *testing.T) {
	withFakeServiceAccountStore(t)
	t.Setenv("SECRET_ENV", "profile-unit-secret")
	flags := &rootFlags{}
	cmd := newRootCmd(flags)
	cmd.SetArgs([]string{"profile", "save", "safe", "--json", "--op-service-account-token-env", "SECRET_ENV", "--op-account", "example"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	p, err := GetProfile("safe")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Values["op-service-account-token-env"]; ok {
		t.Fatal("profile stored token env selector")
	}
	if _, ok := p.Values["op-account"]; ok {
		t.Fatal("profile stored op account selector")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".1password-pp-cli", "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET_ENV") || strings.Contains(string(data), "example") {
		t.Fatalf("profile file contains auth selector material: %s", data)
	}

	// A legacy or hand-edited file is sanitized on read before show/use can
	// reveal it or profile application can consume it.
	store := &profileStore{Profiles: map[string]Profile{"legacy": {Name: "legacy", Values: map[string]string{"json": "true", "token": "legacy-unit-secret", "op-service-account-token-env": "SECRET_ENV"}}}}
	if err := saveProfileStore(store); err != nil {
		t.Fatal(err)
	}
	legacy, err := GetProfile("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Values) != 1 || legacy.Values["json"] != "true" {
		t.Fatalf("legacy profile was not sanitized: %#v", legacy.Values)
	}
	cleaned, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".1password-pp-cli", "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cleaned), "legacy-unit-secret") || strings.Contains(string(cleaned), "SECRET_ENV") {
		t.Fatalf("legacy secret-bearing profile values remained on disk: %s", cleaned)
	}
}

func TestServiceAccountReplacementRejectsUnreadableExistingToken(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "existing-unit-secret"
	fake.getErr = errors.New("Keychain access denied")
	saveTestServiceAccount(t, "team", "example-team.1password.com")
	cmd := newRootCmd(&rootFlags{})
	cmd.SetIn(strings.NewReader("replacement-unit-secret"))
	cmd.SetArgs([]string{"service-accounts", "add", "team", "--account", "example-team.1password.com", "--token-stdin"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "replacement aborted") {
		t.Fatalf("expected preservation failure, got %v", err)
	}
	if fake.setCalls != 0 || len(fake.deleted) != 0 || fake.values["team"] != "existing-unit-secret" {
		t.Fatal("unreadable existing token was changed")
	}
}

func TestServiceAccountDryRunNeverAccessesKeychain(t *testing.T) {
	for _, args := range [][]string{
		{"service-accounts", "add", "team", "--account", "example-team.1password.com", "--dry-run", "--agent"},
		{"service-accounts", "remove", "team", "--dry-run", "--agent"},
		{"service-accounts", "repair-access", "team", "--dry-run", "--agent"},
	} {
		t.Run(args[1], func(t *testing.T) {
			fake := withFakeServiceAccountStore(t)
			fake.values["team"] = "existing-unit-secret"
			fake.getErr = errors.New("Keychain must not be read during preview")
			saveTestServiceAccount(t, "team", "example-team.1password.com")
			metadataPath, err := serviceAccountStorePath()
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			cmd := newRootCmd(&rootFlags{})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || fake.getCalls != 0 || fake.setCalls != 0 || len(fake.deleted) != 0 {
				t.Fatal("dry run accessed Keychain or changed account metadata")
			}
			if !strings.Contains(output.String(), `"dry_run": true`) || strings.Contains(output.String(), "existing-unit-secret") {
				t.Fatal("preview must report dry_run without token data")
			}
		})
	}
}

func TestServiceAccountMetadataFailurePreservesExistingToken(t *testing.T) {
	for _, operation := range []string{"add", "remove"} {
		t.Run(operation, func(t *testing.T) {
			fake := withFakeServiceAccountStore(t)
			fake.values["team"] = "existing-unit-secret"
			saveTestServiceAccount(t, "team", "example-team.1password.com")
			metadataPath, err := serviceAccountStorePath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(metadataPath+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := newRootCmd(&rootFlags{})
			cmd.SetIn(strings.NewReader("replacement-unit-secret"))
			args := []string{"service-accounts", operation, "team"}
			if operation == "add" {
				args = append(args, "--account", "example-team.1password.com", "--token-stdin")
			} else {
				args = append(args, "--yes")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "writing service-account metadata") {
				t.Fatalf("expected metadata failure, got %v", err)
			}
			if fake.values["team"] != "existing-unit-secret" {
				t.Fatal("metadata failure lost original token")
			}
		})
	}
}

func TestServiceAccountRollbackFailureIsReported(t *testing.T) {
	fake := withFakeServiceAccountStore(t)
	fake.values["team"] = "existing-unit-secret"
	fake.failSetAt = 2
	saveTestServiceAccount(t, "team", "example-team.1password.com")
	metadataPath, err := serviceAccountStorePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadataPath+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd(&rootFlags{})
	cmd.SetIn(strings.NewReader("replacement-unit-secret"))
	cmd.SetArgs([]string{"service-accounts", "add", "team", "--account", "example-team.1password.com", "--token-stdin"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "restoring previous Keychain state also failed") {
		t.Fatalf("rollback failure was hidden: %v", err)
	}
}
