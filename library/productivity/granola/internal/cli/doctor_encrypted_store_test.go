// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorUsesLiveMigratedSchemeProbeWithoutSyncState(t *testing.T) {
	support := t.TempDir()
	t.Setenv("GRANOLA_SUPPORT_DIR", support)
	t.Setenv("GRANOLA_SYNC_STATE_PATH", filepath.Join(t.TempDir(), "missing-state.json"))
	t.Setenv("GRANOLA_API_KEY", "")
	if err := os.WriteFile(filepath.Join(support, "cache-v6.json.enc"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{}
	collectEncryptedStoreReport(report)
	got, _ := report["encrypted_store"].(string)
	if !strings.HasPrefix(got, "DEGRADED") {
		t.Fatalf("encrypted_store = %q, want DEGRADED", got)
	}
	hint, _ := report["encrypted_store_hint"].(string)
	if strings.Contains(hint, "Keychain prompt") || strings.Contains(hint, "Always Allow") {
		t.Fatalf("stale Keychain remediation leaked into migrated scheme hint: %q", hint)
	}
}
