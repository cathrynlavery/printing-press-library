// Copyright 2026 Greg Ceccarelli and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanIndeterminateMutationReportsConflictWithoutOverwrite(t *testing.T) {
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"clip":{"cuts":[{"startTimeMs":40,"durationMs":50}]}}`)
		case http.MethodGet:
			fmt.Fprint(w, `{"clip":{"cuts":[{"startTimeMs":40,"durationMs":50}]}}`)
		case http.MethodPatch:
			patches++
			fmt.Fprint(w, `{"clip":{}}`)
		}
	}))
	defer server.Close()

	plan := cleanClipPlan{VideoID: "vid_one", ClipID: "cl_one", Operations: []cleanOperation{{Op: "remove-fillers"}}}
	snapshot := cutSnapshot{VideoID: "vid_one", ClipID: "cl_one", Cuts: []any{map[string]any{"startTimeMs": float64(10), "durationMs": float64(20)}}}
	result, err := applyCleanPlans(fixtureClient(server), []cleanClipPlan{plan}, []cutSnapshot{snapshot})
	if err == nil {
		t.Fatal("expected truncated response to fail")
	}
	if patches != 0 || len(result.Recovery) != 1 || !result.Recovery[0].Conflict || !result.Recovery[0].ManualRestoreRequired || result.RecoveryComplete {
		t.Fatalf("indeterminate result patches=%d result=%#v", patches, result)
	}
}

func TestCleanRollbackSkipsInterveningExternalCuts(t *testing.T) {
	posts := 0
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			posts++
			if posts == 1 {
				fmt.Fprint(w, `{"clip":{"cuts":[{"startTimeMs":40,"durationMs":50}]}}`)
				return
			}
			http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		case http.MethodGet:
			fmt.Fprint(w, `{"clip":{"cuts":[{"startTimeMs":70,"durationMs":80}]}}`)
		case http.MethodPatch:
			patches++
			fmt.Fprint(w, `{"clip":{}}`)
		}
	}))
	defer server.Close()

	plan := cleanClipPlan{VideoID: "vid_one", ClipID: "cl_one", Operations: []cleanOperation{{Op: "remove-fillers"}, {Op: "remove-silences", Mode: "natural"}}}
	snapshot := cutSnapshot{VideoID: "vid_one", ClipID: "cl_one", Cuts: []any{map[string]any{"startTimeMs": float64(10), "durationMs": float64(20)}}}
	result, err := applyCleanPlans(fixtureClient(server), []cleanClipPlan{plan}, []cutSnapshot{snapshot})
	if err == nil || !strings.Contains(err.Error(), "remove-silences") {
		t.Fatalf("apply error = %v", err)
	}
	if patches != 0 || len(result.Recovery) != 1 || !result.Recovery[0].Conflict || !result.Recovery[0].ManualRestoreRequired || result.RecoveryComplete {
		t.Fatalf("external-edit result patches=%d result=%#v", patches, result)
	}
}
