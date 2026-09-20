// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyLegacyProfilesAreSanitizedWithoutWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := profileStorePath()
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"profiles":{"legacy":{"name":"legacy","values":{"json":"true","token":"legacy-test-secret","op-service-account":"other","op-account":"example"}}}}`)
	if err := os.WriteFile(p, legacy, 0o400); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	for _, args := range [][]string{{"profile", "list", "--agent"}, {"profile", "show", "legacy", "--agent"}} {
		var output bytes.Buffer
		cmd := newRootCmd(&rootFlags{})
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v failed on readable legacy store: %v", args, err)
		}
		if strings.Contains(output.String(), "legacy-test-secret") || strings.Contains(output.String(), "op-service-account") || strings.Contains(output.String(), "op-account") {
			t.Fatal("profile read exposed legacy authentication values")
		}
	}
	profile, err := GetProfile("legacy")
	if err != nil || profile == nil || len(profile.Values) != 1 || profile.Values["json"] != "true" {
		t.Fatalf("safe profile values unavailable: profile=%v err=%v", profile, err)
	}
	stored, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(stored, legacy) {
		t.Fatalf("read path changed persistent data: %v", err)
	}
}

func TestExplicitProfileWriteRemovesLegacyAuthenticationValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := &profileStore{Profiles: map[string]Profile{
		"legacy": {Name: "legacy", Values: map[string]string{"json": "true", "token": "legacy-test-secret", "op-account": "example"}},
	}}
	if err := saveProfileStore(store); err != nil {
		t.Fatal(err)
	}
	p, _ := profileStorePath()
	stored, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "legacy-test-secret") || strings.Contains(string(stored), "op-account") || !strings.Contains(string(stored), "json") {
		t.Fatal("explicit write did not persist only safe profile values")
	}
}
