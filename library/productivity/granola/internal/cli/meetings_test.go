// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"testing"

	"github.com/mvanhorn/printing-press-library/library/productivity/granola/internal/granola"
)

func TestFlattenDocKeepsPrivateNotesAndSummarySeparate(t *testing.T) {
	out := flattenDocForJSON(nil, &granola.Document{
		ID:              "note_1",
		NotesMarkdown:   "## Private",
		SummaryMarkdown: "## Generated",
	})
	if out["notes_markdown"] != "## Private" || out["summary_markdown"] != "## Generated" {
		t.Fatalf("flattened document = %#v", out)
	}
}
