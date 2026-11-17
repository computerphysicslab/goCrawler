package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withTempWorkDir runs fn in a temporary directory with a logs/ folder.
func withTempWorkDir(t *testing.T, fn func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	for name := range rejectedLinkSets {
		rejectedLinkSets[name] = make(map[string]struct{})
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

// TestTokenize checks token splitting and counting.
func TestTokenize(t *testing.T) {
	got := tokenize("Hello, world! stack-ing 123")
	want := []string{"Hello", "world", "stack", "ing", "123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
	if tokensCount("a b c") != 3 {
		t.Fatal("tokensCount should count three tokens")
	}
}

// TestSplitParagraphs checks paragraph splitting on line breaks.
func TestSplitParagraphs(t *testing.T) {
	got := splitParagraphs("first\n\nsecond\r\nthird")
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitParagraphs = %v, want %v", got, want)
	}
}

// TestGetDomainAndSecondLevelDomain checks host extraction.
func TestGetDomainAndSecondLevelDomain(t *testing.T) {
	if got := getDomain("https://www.siril.org/tutorials/"); got != "siril.org" {
		t.Fatalf("getDomain = %q, want siril.org", got)
	}
	if got := getSecondLevelDomain("https://www.siril.org/tutorials/"); got != "siril.org" {
		t.Fatalf("getSecondLevelDomain = %q, want siril.org", got)
	}
}

// TestRankingByKeywords checks keyword-based scoring.
func TestRankingByKeywords(t *testing.T) {
	setupTestConfig()
	text := "stack image noise signal siril stacking"
	if got := rankingByKeywords(text); got <= 0 {
		t.Fatalf("rankingByKeywords = %v, want > 0", got)
	}
	if got := rankingByKeywords("unrelated words without keywords"); got != 0 {
		t.Fatalf("rankingByKeywords without keywords = %v, want 0", got)
	}
}

// TestAnalyzeFiltersStopwords verifies that stopwords are removed.
func TestAnalyzeFiltersStopwords(t *testing.T) {
	setupTestConfig()
	tokens := analyze("The signal and the noise of the image")
	joined := strings.Join(tokens, " ")
	if !strings.Contains(joined, "signal") || !strings.Contains(joined, "noise") {
		t.Fatalf("analyze dropped meaningful tokens: %v", tokens)
	}
	if strings.Contains(joined, "the") {
		t.Fatalf("analyze kept stopwords: %v", tokens)
	}
}

// TestBestParagraph verifies selection of the highest-ranked paragraph.
func TestBestParagraph(t *testing.T) {
	setupTestConfig()
	best := bestParagraph([]string{
		"ordinary text with no relevant terms at all",
		"stack image noise signal siril stacking",
	})
	if !strings.Contains(best, "stack") {
		t.Fatalf("bestParagraph = %q, want the stacking paragraph", best)
	}
}

// TestFreqAndSort verifies frequency counting and sorting.
func TestFreqAndSort(t *testing.T) {
	setupTestConfig()
	f := make(freq)
	for _, token := range []string{"alpha", "beta", "beta", "gamma", "gamma", "gamma"} {
		f.add(token)
	}
	sorted := rSortFreq(f)
	if got := getKVkeys(sorted); !reflect.DeepEqual(got, []string{"gamma", "beta", "alpha"}) {
		t.Fatalf("rSortFreq keys = %v, want [gamma beta alpha]", got)
	}
}

// TestNgramsOf checks n-gram extraction.
func TestNgramsOf(t *testing.T) {
	setupTestConfig()
	got := ngramsOf("stack image noise", 2)
	if len(got) == 0 {
		t.Fatal("ngramsOf returned no bigrams")
	}
	if !strings.Contains(strings.Join(got, "|"), "stack") {
		t.Fatalf("ngramsOf = %v, expected stack bigrams", got)
	}
}

// TestLinkSeemsOk checks the URL validation chain.
func TestLinkSeemsOk(t *testing.T) {
	setupTestConfig()
	withTempWorkDir(t, func() {
		if !linkSeemsOk("https://siril.org/tutorials/stacking", false) {
			t.Fatal("whitelisted URL should pass")
		}
		if !linkSeemsOk("https://example.org/about", true) {
			t.Fatal("syntactically valid URL should pass when custom checks are skipped")
		}
		if linkSeemsOk("abc", false) {
			t.Fatal("too short URL should fail")
		}
		if linkSeemsOk("javascript:void(0)", false) {
			t.Fatal("javascript URL should fail")
		}
		if linkSeemsOk("https://facebook.com/profile", false) {
			t.Fatal("banned domain should fail")
		}
		if !iolibFileExists("logs/linksNotOk.log") {
			t.Fatal("rejected links should be logged")
		}
	})
}

// iolibFileExists is a small local helper to avoid touching the real working directory.
func iolibFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestDomainHadFailed checks failure counting without regex injection.
func TestDomainHadFailed(t *testing.T) {
	withTempWorkDir(t, func() {
		content := strings.Repeat("example.org\n", 7) + "a(b).com\n"
		if err := os.WriteFile("logs/domainFailed.log", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if !domainHadFailed("example.org") {
			t.Fatal("domain with 7 failures should be discarded")
		}
		if domainHadFailed("other.org") {
			t.Fatal("domain without failures should be allowed")
		}
		if domainHadFailed("a(b).com") {
			t.Fatal("regex metacharacters in a domain must not cause a false positive")
		}
	})
}

// TestLogRejectedLinkDeduplicates verifies log deduplication.
func TestLogRejectedLinkDeduplicates(t *testing.T) {
	withTempWorkDir(t, func() {
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		logRejectedLink("linksNotOk.log", "https://example.org/bad")
		b, err := os.ReadFile("logs/linksNotOk.log")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(b), "https://example.org/bad"); got != 1 {
			t.Fatalf("link was written %d times, want 1", got)
		}
	})
}
