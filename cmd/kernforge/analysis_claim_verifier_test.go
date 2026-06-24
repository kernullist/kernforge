package main

import "testing"

func claimIssueHasCode(issues []ClaimVerificationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func claimIssueSeverity(issues []ClaimVerificationIssue, code string) string {
	for _, issue := range issues {
		if issue.Code == code {
			return issue.Severity
		}
	}
	return ""
}

// TestVerifyClaimSourceAnchorsCaseInsensitivePathRunsLineCheck locks CLAIM-1: a
// case/format-divergent but valid anchor path must still match its cited packet
// (case-insensitively) so the blocking line-range check actually runs. Before the
// fix an exact-case comparison demoted a fabricated line number to a non-blocking
// source_packet_mismatch warning.
func TestVerifyClaimSourceAnchorsCaseInsensitivePathRunsLineCheck(t *testing.T) {
	shard := AnalysisShard{ID: "s1", PrimaryFiles: []string{"src/driver.c"}}
	packets := []EvidencePacket{{ID: "p1", Path: "src/driver.c", StartLine: 1, EndLine: 5}}

	// Fabricated line 10 (outside 1..5) cited with a divergent-case path.
	bad := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"Src/Driver.c:10"}},
		packets, shard, SemanticIndexV2{}, false,
	)
	if !claimIssueHasCode(bad, "line_range_mismatch") {
		t.Fatalf("a divergent-case valid path with a fabricated line must block via line_range_mismatch, got %#v", bad)
	}
	if claimIssueHasCode(bad, "source_packet_mismatch") {
		t.Fatalf("a divergent-case path must not be demoted to a packet-mismatch warning, got %#v", bad)
	}
	if got := claimIssueSeverity(bad, "line_range_mismatch"); got != "blocking" {
		t.Fatalf("line_range_mismatch must be blocking, got %q", got)
	}

	// A correct in-range line on the same divergent-case path must verify clean.
	good := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"Src/Driver.c:3"}},
		packets, shard, SemanticIndexV2{}, false,
	)
	if claimIssueHasCode(good, "line_range_mismatch") || claimIssueHasCode(good, "source_packet_mismatch") || claimIssueHasCode(good, "source_scope_mismatch") {
		t.Fatalf("an in-range divergent-case anchor must verify clean, got %#v", good)
	}
}

// TestPacketPathInShardScopeEmptyScopeFailsClosed locks CLAIM-2: a shard with no
// assigned primary/reference files is out of scope (fail-closed), not accept-all,
// and scope matching is case/separator-insensitive.
func TestPacketPathInShardScopeEmptyScopeFailsClosed(t *testing.T) {
	if packetPathInShardScope("foo.c", AnalysisShard{}) {
		t.Fatalf("an empty-scope shard must be fail-closed (out of scope)")
	}
	if !packetPathInShardScope("Src/Foo.c", AnalysisShard{PrimaryFiles: []string{"src/foo.c"}}) {
		t.Fatalf("a case-divergent in-scope path must match")
	}
	if packetPathInShardScope("src/other.c", AnalysisShard{PrimaryFiles: []string{"src/foo.c"}}) {
		t.Fatalf("an out-of-scope path must not match")
	}
	if !packetPathInShardScope("ref/util.c", AnalysisShard{PrimaryFiles: []string{"src/foo.c"}, ReferenceFiles: []string{"ref/util.c"}}) {
		t.Fatalf("a reference-scope path must match")
	}
}

// TestVerifyClaimSourceAnchorsFlagsUnparseableAndMissing locks CLAIM-3: blank-only
// anchors are treated as missing (the gate no longer keys on raw slice length),
// and a non-blank but unparseable anchor is flagged instead of silently dropped.
func TestVerifyClaimSourceAnchorsFlagsUnparseableAndMissing(t *testing.T) {
	shard := AnalysisShard{ID: "s1", PrimaryFiles: []string{"src/driver.c"}}

	blank := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "medium", SourceAnchors: []string{"", "   "}},
		nil, shard, SemanticIndexV2{}, false,
	)
	if !claimIssueHasCode(blank, "missing_source_anchor") {
		t.Fatalf("a slice of blank anchors must be treated as missing, got %#v", blank)
	}

	garbage := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "medium", SourceAnchors: []string{"``"}},
		nil, shard, SemanticIndexV2{}, false,
	)
	if !claimIssueHasCode(garbage, "unparseable_source_anchor") {
		t.Fatalf("a non-blank unparseable anchor must be flagged, got %#v", garbage)
	}

	// High-confidence escalates the unparseable warning to blocking.
	garbageHigh := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"``"}},
		nil, shard, SemanticIndexV2{}, false,
	)
	if got := claimIssueSeverity(garbageHigh, "unparseable_source_anchor"); got != "blocking" {
		t.Fatalf("a high-confidence unparseable anchor must be blocking, got %q", got)
	}
}

// TestVerifyClaimSourceAnchorsStartLineZeroEnforcesKnownBound locks CLAIM-5: a
// partially populated packet (StartLine <= 0 but EndLine > 0) must still enforce
// its known upper bound rather than accept any cited line; only a packet with no
// line info at all (both bounds unset) bypasses the range check.
func TestVerifyClaimSourceAnchorsStartLineZeroEnforcesKnownBound(t *testing.T) {
	shard := AnalysisShard{ID: "s1", PrimaryFiles: []string{"src/driver.c"}}

	partial := []EvidencePacket{{ID: "p1", Path: "src/driver.c", StartLine: 0, EndLine: 5}}
	over := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"src/driver.c:9999"}},
		partial, shard, SemanticIndexV2{}, false,
	)
	if !claimIssueHasCode(over, "line_range_mismatch") {
		t.Fatalf("a partial packet (EndLine set) must still reject an out-of-bound line, got %#v", over)
	}

	noLineInfo := []EvidencePacket{{ID: "p1", Path: "src/driver.c", StartLine: 0, EndLine: 0}}
	any := verifyClaimSourceAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"src/driver.c:9999"}},
		noLineInfo, shard, SemanticIndexV2{}, false,
	)
	if claimIssueHasCode(any, "line_range_mismatch") {
		t.Fatalf("a packet with no line info at all must bypass the range check, got %#v", any)
	}
}

// TestVerifyClaimSymbolAnchorsCaseInsensitivePathScopesIndex locks CLAIM-4: the
// structural-index symbol scope must be matched against packet paths
// case/separator-insensitively (matching CLAIM-1's analysisClaimPathKey). Before
// the fix an exact-case packetPaths key dropped the index symbol augmentation
// whenever the index's symbol.File diverged in case/separator from the
// canonicalized packet path, producing a blocking symbol_mismatch false-reject
// for a symbol that the index actually scopes to that file.
func TestVerifyClaimSymbolAnchorsCaseInsensitivePathScopesIndex(t *testing.T) {
	// Packet carries no symbol of its own; the cited symbol exists only in the
	// structural index, keyed by a divergent-case form of the same file.
	packets := []EvidencePacket{{ID: "p1", Path: "src/driver.c"}}
	index := SemanticIndexV2{Symbols: []SymbolRecord{{Name: "DriverEntry", File: "Src/Driver.c"}}}

	scoped := verifyClaimSymbolAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"src/driver.c#DriverEntry"}},
		packets, index,
	)
	if claimIssueHasCode(scoped, "symbol_mismatch") {
		t.Fatalf("a symbol scoped by the index on a divergent-case path must not be a blocking mismatch, got %#v", scoped)
	}

	// A symbol the index does not scope to any cited packet path must still be
	// flagged, so the fix loosens case only -- not the mismatch gate itself.
	absent := verifyClaimSymbolAnchors(
		AnalysisClaim{Confidence: "high", SourceAnchors: []string{"src/driver.c#GhostRoutine"}},
		packets, index,
	)
	if !claimIssueHasCode(absent, "symbol_mismatch") {
		t.Fatalf("a symbol absent from packets and index scope must still be a mismatch, got %#v", absent)
	}
}
