// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptGetUsesOfficialPaginatedEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GRANOLA_CACHE_PATH", filepath.Join(home, "missing-cache.json"))
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/notes/not_live/transcript" {
			t.Errorf("path = %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		if r.URL.Query().Get("cursor") == "next" {
			_, _ = w.Write([]byte(`{"transcript":[{"text":"second page","start_time":"2026-01-01T00:00:01Z","end_time":"2026-01-01T00:00:02Z","speaker":{"source":"speaker","attribution":"them"}}],"hasMore":false,"cursor":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"transcript":[{"text":"first page","start_time":"2026-01-01T00:00:00Z","end_time":"2026-01-01T00:00:01Z","speaker":{"source":"microphone","attribution":"me"}}],"hasMore":true,"cursor":"next"}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)

	out, _, err := runCLISplit(t, "transcript", "get", "not_live", "--json")
	if err != nil {
		t.Fatalf("transcript get: %v (out=%s)", err, out)
	}
	var envelope struct {
		Source   string `json:"source"`
		Segments []struct {
			Text        string `json:"text"`
			Source      string `json:"source"`
			Attribution string `json:"attribution"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode output: %v (%s)", err, out)
	}
	if envelope.Source != "live" || len(envelope.Segments) != 2 || envelope.Segments[1].Source != "system" || envelope.Segments[1].Attribution != "them" {
		t.Fatalf("output = %+v", envelope)
	}
	if strings.Join(cursors, ",") != ",next" {
		t.Fatalf("cursors = %v", cursors)
	}
}

func TestTranscriptGetLegacyIDSkipsPublicAPIWithExplicitLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GRANOLA_CACHE_PATH", filepath.Join(home, "missing-cache.json"))
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")
	publicRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicRequests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)

	_, _, err := runCLISplit(t, "transcript", "get", "196037d9-7d28-4d4d-9c4f-c0e7e95b1aaa", "--data-source", "live", "--json")
	if err == nil {
		t.Fatal("expected missing internal session to fail")
	}
	if publicRequests != 0 {
		t.Fatalf("legacy UUID made %d public API request(s), want 0", publicRequests)
	}
	if strings.Contains(err.Error(), "after setting GRANOLA_API_KEY") {
		t.Fatalf("explicit live request stopped before the internal fallback: %v", err)
	}
}

func TestNotesListSendsFolderFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("folder_id"); got != "folder_1" {
			t.Errorf("folder_id = %q", got)
		}
		_, _ = w.Write([]byte(`{"notes":[],"hasMore":false,"cursor":null}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	_, _, err := runCLISplit(t, "notes", "list", "--folder-id", "folder_1", "--json")
	if err != nil {
		t.Fatalf("notes list: %v", err)
	}
}

func TestNotesListAllFollowsCursor(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		if r.URL.Query().Get("cursor") == "page_2" {
			_, _ = w.Write([]byte(`{"notes":[{"id":"note_2"}],"hasMore":false,"cursor":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"notes":[{"id":"note_1"}],"hasMore":true,"cursor":"page_2"}`))
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	out, _, err := runCLISplit(t, "notes", "list", "--all", "--json")
	if err != nil {
		t.Fatalf("notes list --all: %v", err)
	}
	if strings.Join(cursors, ",") != ",page_2" || !strings.Contains(out, "note_1") || !strings.Contains(out, "note_2") {
		t.Fatalf("cursors=%v out=%s", cursors, out)
	}
}

func TestNotesGetFallsBackAfterEmbeddedTranscript413(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch {
		case r.URL.Path == "/v1/notes/note_large" && r.URL.Query().Get("include") == "transcript":
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"TRANSCRIPT_TOO_LARGE"}`))
		case r.URL.Path == "/v1/notes/note_large":
			_, _ = w.Write([]byte(`{"id":"note_large","title":"Long meeting"}`))
		case r.URL.Path == "/v1/notes/note_large/transcript":
			_, _ = w.Write([]byte(`{"transcript":[{"text":"paged transcript"}],"hasMore":false,"cursor":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("GRANOLA_BASE_URL", srv.URL)
	t.Setenv("GRANOLA_API_KEY", "grn_test_key")

	out, _, err := runCLISplit(t, "notes", "get", "note_large", "--include", "transcript", "--data-source", "live", "--json")
	if err != nil {
		t.Fatalf("notes get: %v (out=%s)", err, out)
	}
	if len(paths) != 3 || !strings.Contains(out, "paged transcript") {
		t.Fatalf("paths=%v out=%s", paths, out)
	}
}
