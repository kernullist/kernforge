package main

import (
	"reflect"
	"testing"
)

// TestParsePatchDocumentAcceptsNewFileHeaderAlias locks the header-synonym
// tolerance: a model that writes "*** New File:" instead of "*** Add File:"
// still produces a valid add operation.
func TestParsePatchDocumentAcceptsNewFileHeaderAlias(t *testing.T) {
	patch := "*** Begin Patch\n*** New File: notes.md\n+# Title\n+body\n*** End Patch"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("New File header alias should parse, got %v", err)
	}
	if len(doc.ops) != 1 || doc.ops[0].kind != "add" || doc.ops[0].path != "notes.md" {
		t.Fatalf("expected one add op for notes.md, got %+v", doc.ops)
	}
	if want := []string{"# Title", "body"}; !reflect.DeepEqual(doc.ops[0].addLines, want) {
		t.Fatalf("add lines = %#v, want %#v", doc.ops[0].addLines, want)
	}
}

func TestParsePatchDocumentAcceptsRemoveFileHeaderAlias(t *testing.T) {
	patch := "*** Begin Patch\n*** Remove File: stale.txt\n*** End Patch"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("Remove File header alias should parse, got %v", err)
	}
	if len(doc.ops) != 1 || doc.ops[0].kind != "delete" || doc.ops[0].path != "stale.txt" {
		t.Fatalf("expected one delete op for stale.txt, got %+v", doc.ops)
	}
}

// TestParseAddFileToleratesBareBlankLine locks the blank-line tolerance: a blank
// line in a new file emitted as a bare empty line (not "+") is treated as an
// empty added line instead of failing the whole patch.
func TestParseAddFileToleratesBareBlankLine(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: doc.md\n+line one\n\n+line three\n*** End Patch"
	doc, err := parsePatchDocument(patch)
	if err != nil {
		t.Fatalf("a bare blank line in an add body should be tolerated, got %v", err)
	}
	if want := []string{"line one", "", "line three"}; len(doc.ops) != 1 || !reflect.DeepEqual(doc.ops[0].addLines, want) {
		t.Fatalf("add lines = %#v, want %#v", doc.ops[0].addLines, want)
	}
}

// TestParseAddFileRejectsNonPlusContentLine keeps the tolerance narrow: a
// non-empty line that lost its "+" is still an error, so content is never
// silently swallowed.
func TestParseAddFileRejectsNonPlusContentLine(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: doc.md\n+ok\nnot-a-plus-line\n*** End Patch"
	if _, err := parsePatchDocument(patch); err == nil {
		t.Fatal("a non-empty line without a leading + must still be rejected")
	}
}
