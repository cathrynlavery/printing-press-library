// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mockDocumentOp(t *testing.T, ref, kind, typeExit, contentExit string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "$DOC_TEST_LOG"
[ "$#" = 2 ] && [ "$1" = read ] || exit 91
case "$2" in
  "$DOC_TEST_REF?attribute=type")
    printf '%s\n' "$DOC_TEST_TYPE"
    exit "$DOC_TEST_TYPE_EXIT"
    ;;
  "$DOC_TEST_REF?attribute=content")
    printf '%s' 'synthetic document bytes'
    exit "$DOC_TEST_CONTENT_EXIT"
    ;;
  *) exit 92 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRINTING_PRESS_VERIFY", "")
	t.Setenv("DOC_TEST_LOG", log)
	t.Setenv("DOC_TEST_REF", ref)
	t.Setenv("DOC_TEST_TYPE", kind)
	t.Setenv("DOC_TEST_TYPE_EXIT", typeExit)
	t.Setenv("DOC_TEST_CONTENT_EXIT", contentExit)
	return log
}

func TestDocumentsReadChecksExactReferenceTypeBeforeContent(t *testing.T) {
	for _, tc := range []struct {
		name, ref, kind, typeExit, contentExit string
		wantError                              bool
		wantContentCall                        bool
	}{
		{"document", "op://Synthetic/Document/report.pdf", "file", "0", "0", false, true},
		{"section attachment", "op://Synthetic/Login/Attachments/certificate.pem", "file", "0", "0", false, true},
		{"file identifier", "op://vault-id/item-id/file-id", "FILE", "0", "0", false, true},
		{"password", "op://Synthetic/Login/password", "concealed", "0", "0", true, false},
		{"text named as document", "op://Synthetic/Login/service-account.json", "string", "0", "0", true, false},
		{"empty metadata", "op://Synthetic/Document/report.pdf", "", "0", "0", true, false},
		{"unknown metadata", "op://Synthetic/Document/report.pdf", "unknown", "0", "0", true, false},
		{"malformed metadata", "op://Synthetic/Document/report.pdf", "file\nconcealed", "0", "0", true, false},
		{"type lookup failure", "op://Synthetic/Document/report.pdf", "file", "1", "0", true, false},
		{"content lookup failure", "op://Synthetic/Document/report.pdf", "file", "0", "1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := mockDocumentOp(t, tc.ref, tc.kind, tc.typeExit, tc.contentExit)
			cmd := newNovelDocumentsReadCmd(&rootFlags{})
			cmd.SetContext(context.WithValue(context.Background(), opAuthContextKey{}, opAuthContext{}))
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{tc.ref, "--reveal"})
			err := cmd.Execute()
			if (err != nil) != tc.wantError {
				t.Fatalf("Execute error = %v; wantError = %v", err, tc.wantError)
			}
			wantOut := "synthetic document bytes"
			if tc.wantError {
				wantOut = ""
			}
			if out.String() != wantOut {
				t.Fatalf("stdout = %q; want %q", out.String(), wantOut)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := "read " + tc.ref + "?attribute=type\n"
			if tc.wantContentCall {
				wantCalls += "read " + tc.ref + "?attribute=content\n"
			}
			if string(calls) != wantCalls {
				t.Fatalf("op calls = %q; want %q", calls, wantCalls)
			}
		})
	}
}

func TestDocumentsReadGatesDoNotReadMetadataOrContent(t *testing.T) {
	for _, tc := range []struct {
		name, ref                     string
		reveal, dryRun, verify, error bool
	}{
		{"no reveal", "op://Synthetic/Document/report.pdf", false, false, false, false},
		{"dry run", "op://Synthetic/Document/report.pdf", true, true, false, false},
		{"verify", "op://Synthetic/Document/report.pdf", true, false, true, false},
		{"production policy", "op://Production/Document/report.pdf", true, false, false, true},
		{"card policy", "op://Synthetic/Card/number", true, false, false, true},
		{"query override", "op://Synthetic/Login/password?attribute=value", true, false, false, true},
		{"metadata query", "op://Synthetic/Document/report.pdf?attribute=type", true, false, false, true},
		{"empty query", "op://Synthetic/Document/report.pdf?", true, false, false, true},
		{"missing field", "op://Synthetic/Document/", true, false, false, true},
		{"empty section", "op://Synthetic/Document//report.pdf", true, false, false, true},
		{"extra segment", "op://Synthetic/Document/section/nested/report.pdf", true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := mockDocumentOp(t, tc.ref, "file", "0", "0")
			if tc.verify {
				t.Setenv("PRINTING_PRESS_VERIFY", "1")
			}
			cmd := newNovelDocumentsReadCmd(&rootFlags{dryRun: tc.dryRun, asJSON: true})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SilenceUsage = true
			args := []string{tc.ref}
			if tc.reveal {
				args = append(args, "--reveal")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if (err != nil) != tc.error {
				t.Fatalf("Execute error = %v; wantError = %v", err, tc.error)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 0 || strings.Contains(output.String(), "synthetic document bytes") {
				t.Fatalf("gate invoked op or exposed content: calls=%q output=%q", calls, output.String())
			}
		})
	}
}
