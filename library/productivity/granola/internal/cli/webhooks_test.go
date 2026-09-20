// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebhooksCreatePreservesOneTimeSecret(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/webhook-endpoints" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"id":"whe_1","signing_secret":"whsec_c3ludGhldGlj","url_redacted":"https://example.test/***"}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	out, _, err := runCLISplit(t, "webhooks", "create", "--url", "https://example.test/hook", "--scope", "personal", "--event", "note.edited", "--agent")
	if err != nil {
		t.Fatalf("webhooks create: %v (out=%s)", err, out)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode response: %v (%s)", err, out)
	}
	if response["signing_secret"] != "whsec_c3ludGhldGlj" {
		t.Fatalf("one-time secret was compacted away: %s", out)
	}
	if got := body["url"]; got != "https://example.test/hook" {
		t.Fatalf("url = %#v", got)
	}
}

func TestWebhooksCreateHonorsQuietAndSelect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"whe_1","signing_secret":"whsec_c3ludGhldGlj","url_redacted":"https://example.test/***"}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	quietOut, _, err := runCLISplit(t, "webhooks", "create", "--url", "https://example.test/hook", "--scope", "personal", "--quiet")
	if err != nil {
		t.Fatalf("webhooks create --quiet: %v", err)
	}
	if quietOut != "" {
		t.Fatalf("--quiet output = %q, want empty", quietOut)
	}

	selectedOut, _, err := runCLISplit(t, "webhooks", "create", "--url", "https://example.test/hook", "--scope", "personal", "--select", "id", "--json")
	if err != nil {
		t.Fatalf("webhooks create --select id: %v", err)
	}
	if strings.Contains(selectedOut, "signing_secret") || strings.Contains(selectedOut, "whsec_") {
		t.Fatalf("--select id leaked signing secret: %s", selectedOut)
	}
	if !strings.Contains(selectedOut, `"id"`) {
		t.Fatalf("--select id output = %s", selectedOut)
	}
}

func TestWebhooksUpdateCanClearFoldersAndDisable(t *testing.T) {
	const endpointID = "whe_1234567890ABCD"
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/webhook-endpoints/"+endpointID {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"id":"whe_1234567890ABCD","enabled":false,"folder_ids":[]}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	_, _, err := runCLISplit(t, "webhooks", "update", endpointID, "--clear-folders", "--enabled=false", "--json")
	if err != nil {
		t.Fatalf("webhooks update: %v", err)
	}
	if enabled, ok := body["enabled"].(bool); !ok || enabled {
		t.Fatalf("enabled = %#v", body["enabled"])
	}
	if folders, ok := body["folder_ids"].([]any); !ok || len(folders) != 0 {
		t.Fatalf("folder_ids = %#v", body["folder_ids"])
	}
}

func TestWebhooksDeleteRequiresConfirmation(t *testing.T) {
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")
	_, _, err := runCLISplit(t, "webhooks", "delete", "whe_1234567890ABCD")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateWebhookFilters(t *testing.T) {
	if err := validateWebhookFilters([]string{"workspace"}, []string{"note.generated"}, []string{"fol_1234567890ABCD"}, true); err != nil {
		t.Fatalf("valid workspace filter: %v", err)
	}
	if err := validateWebhookFilters([]string{"workspace", "personal"}, nil, nil, true); err == nil {
		t.Fatal("expected mixed workspace scope to fail")
	}
	if err := validateWebhookFilters([]string{"personal"}, nil, []string{"folder_1"}, true); err == nil {
		t.Fatal("expected malformed folder id to fail")
	}
}

func TestWebhooksVerifyChecksRawBodyOffline(t *testing.T) {
	secretBytes := []byte("synthetic-signing-key")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(secretBytes)
	t.Setenv("GRANOLA_WEBHOOK_SECRET", secret)
	body := []byte(`{"event_id":"evt_test","type":"note.generated","data":{"note_id":"note_1"}}`)
	bodyPath := filepath.Join(t.TempDir(), "delivery.json")
	if err := os.WriteFile(bodyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, secretBytes)
	_, _ = fmt.Fprintf(mac, "evt_test.%s.", timestamp)
	_, _ = mac.Write(body)
	signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	out, _, err := runCLISplit(t, "webhooks", "verify",
		"--webhook-id", "evt_test",
		"--webhook-timestamp", timestamp,
		"--webhook-signature", signature,
		"--body-file", bodyPath,
		"--json")
	if err != nil {
		t.Fatalf("webhooks verify: %v (out=%s)", err, out)
	}
	if !strings.Contains(out, `"verified": true`) {
		t.Fatalf("output = %s", out)
	}
}
