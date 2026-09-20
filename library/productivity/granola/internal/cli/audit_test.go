// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditListUsesDedicatedKeyAndPaginatesSerially(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer audit_test_key" {
			t.Errorf("Authorization = %q", got)
		}
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		if r.URL.Query().Get("action") != "workspace" || r.URL.Query().Get("occurred_after") != "2026-09-01" {
			t.Errorf("filters = %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("cursor") == "audit_next" {
			_, _ = w.Write([]byte(`{"events":[{"id":"evt_2","action":"note.viewed"}],"hasMore":false,"cursor":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"events":[{"id":"evt_1","action":"note.created"}],"hasMore":true,"cursor":"audit_next"}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "regular_key_must_not_be_used")
	t.Setenv("GRANOLA_AUDIT_API_KEY", "audit_test_key")

	out, _, err := runCLISplit(t, "audit", "list", "--action", "workspace", "--occurred-after", "2026-09-01", "--all", "--json")
	if err != nil {
		t.Fatalf("audit list: %v (out=%s)", err, out)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("decode output: %v (%q)", err, out)
	}
	if len(events) != 2 || len(cursors) != 2 || cursors[1] != "audit_next" {
		t.Fatalf("events=%v cursors=%v", events, cursors)
	}
}

func TestAuditListRequiresDedicatedKey(t *testing.T) {
	t.Setenv("GRANOLA_AUDIT_API_KEY", "")
	_, _, err := runCLISplit(t, "audit", "list")
	if err == nil {
		t.Fatal("expected missing audit key error")
	}
}
