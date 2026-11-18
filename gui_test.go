package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withGUIApp installs a fresh crawler for a test and restores the previous one.
func withGUIApp(t *testing.T, fn func()) {
	t.Helper()
	old := app
	defer func() { app = old }()
	app = newCrawler()
	fn()
}

func guiRequest(t *testing.T, server *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestGUIRootAndAssets(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/", "")
		if code != 200 || !strings.Contains(body, "goCrawler Workbench") {
			t.Fatalf("root = %d %q", code, body[:minInt(120, len(body))])
		}
		for _, fragment := range []string{"/api/keywords/apply", "'/api/stats'", "btn-start", "btn-stop", "s-running", "reasons", `id="pr-profile"`, "<select id=\"pr-profile\""} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML is missing %q", fragment)
			}
		}
		if !strings.Contains(body, `id="btn-stop" class="act" onclick="stopCrawl()" disabled`) {
			t.Fatal("Stop button should start disabled")
		}
		prof := strings.Index(body, `data-tab="prof"`)
		dash := strings.Index(body, `data-tab="dash"`)
		keys := strings.Index(body, `data-tab="keys"`)
		corpus := strings.Index(body, `data-tab="corpus"`)
		promptTab := strings.Index(body, `data-tab="prompt"`)
		if prof < 0 || dash < prof || keys < dash || corpus < keys || promptTab < corpus {
			t.Fatalf("tab order wrong: prof=%d dash=%d keys=%d corpus=%d prompt=%d", prof, dash, keys, corpus, promptTab)
		}
		if !strings.Contains(body, `<section id="prof"><div class="card">`) || !strings.Contains(body, `<section id="dash" hidden>`) {
			t.Fatal("Profiles should be the visible default tab")
		}
	})
}

func TestGUIProfilesEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "gui_list_test.yaml")
		if err := os.WriteFile(path, []byte("scoreThreshold: 1\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/profiles", "")
		if code != 200 || !strings.Contains(body, "gui_list_test") {
			t.Fatalf("profiles = %d %q", code, body)
		}
	})
}

func TestGUIProfileGetAndSave(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()

		profilePath := filepath.Join("profiles", "gui_get_test.yaml")
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(profilePath, []byte("scoreThreshold: 7\nlinkOk: |\n  stack\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(profilePath)
		code, body := guiRequest(t, server, "GET", "/api/profile?name=gui_get_test", "")
		if code != 200 {
			t.Fatalf("profile GET = %d %q", code, body)
		}
		var payload struct{ Content string }
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Content, "scoreThreshold") {
			t.Fatalf("profile content missing scoreThreshold: %q", payload.Content[:120])
		}

		name := "gui_test_profile"
		path := filepath.Join("profiles", name+".yaml")
		defer os.Remove(path)
		save := `{"name":"` + name + `","content":"scoreThreshold: 7\n"}`
		code, body = guiRequest(t, server, "POST", "/api/profile", save)
		if code != 200 || !strings.Contains(body, "saved") {
			t.Fatalf("profile POST = %d %q", code, body)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "scoreThreshold: 7\n" {
			t.Fatalf("saved profile = %q, err=%v", got, err)
		}
	})
}

func TestGUIStatsEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		app.CacheHits = 7
		app.OnlineDownloads = 3
		app.DownloadMS = 1234
		app.AcceptedDocs = 5
		app.GUILastError = "sample error"
		app.RejectionCounts = map[string]int{"non_english": 12, "too_short": 4}
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/stats", "")
		if code != 200 {
			t.Fatalf("stats = %d", code)
		}
		var stats map[string]interface{}
		if err := json.Unmarshal([]byte(body), &stats); err != nil {
			t.Fatal(err)
		}
		if stats["visits"].(float64) != 10 || stats["cache"].(float64) != 7 ||
			stats["online"].(float64) != 3 || stats["accepted"].(float64) != 5 ||
			stats["error"].(string) != "sample error" || stats["not_accepted"].(float64) != 5 ||
			stats["running_count"].(float64) != 0 {
			t.Fatalf("unexpected stats: %v", stats)
		}
		reasons, ok := stats["rejections"].(map[string]interface{})
		if !ok || reasons["non_english"].(float64) != 12 || reasons["too_short"].(float64) != 4 {
			t.Fatalf("unexpected rejection breakdown: %v", stats["rejections"])
		}
	})
}

func TestGUIDocumentsEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/documents", "")
		if code != 200 {
			t.Fatalf("documents = %d %q", code, body[:minInt(120, len(body))])
		}
		if body != "[]\n" && (!strings.Contains(body, "url") || !strings.Contains(body, "text")) {
			t.Fatalf("unexpected documents payload: %q", body[:minInt(120, len(body))])
		}
	})
}

func TestGUISearchEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		oldPath := app.CachePath
		app.CachePath = filepath.Join(t.TempDir(), "search.db")
		defer func() {
			if app.SearchDB != nil {
				app.SearchDB.Close()
				app.SearchDB = nil
			}
			app.CachePath = oldPath
		}()
		if err := initSearchIndex(); err != nil {
			t.Fatal(err)
		}
		app.SearchDB.Exec(`INSERT INTO corpus_fts(url,text) VALUES(?,?)`, "https://example.org/stack", "stacking images of galaxies")
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/search?q=stacking", "")
		if code != 200 || !strings.Contains(body, "example.org/stack") {
			t.Fatalf("search = %d %q", code, body)
		}
	})
}

func TestGUIKeywordsExtractAndApply(t *testing.T) {
	withGUIApp(t, func() {
		name := "gui_keyword_test"
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", name+".yaml")
		original := "linkOk: |\n  old\nregexRankingKeywords: |\n  (?i)\\W(old)\\W\n"
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/keywords?profile="+name, "")
		if code != 200 || !strings.Contains(body, "keywords") || !strings.Contains(body, "details") {
			t.Fatalf("keywords = %d %q", code, body)
		}
		for _, stop := range []string{`"cannot"`, `"not"`, `"these"`, `"with"`} {
			if strings.Contains(body, stop) {
				t.Fatalf("stopword %s leaked into extracted keywords", stop)
			}
		}
		apply := `{"profile":"` + name + `","keywords":["stacking","drizzle"]}`
		code, body = guiRequest(t, server, "POST", "/api/keywords/apply", apply)
		if code != 200 || !strings.Contains(body, "applied") {
			t.Fatalf("apply = %d %q", code, body)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "stacking|drizzle") || strings.Contains(string(got), "old") {
			t.Fatalf("applied profile = %q", got)
		}
	})
}

func TestGUICrawlStartStopValidation(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()

		code, _ := guiRequest(t, server, "GET", "/api/crawl", "")
		if code != 405 {
			t.Fatalf("GET /api/crawl = %d, want 405", code)
		}
		code, body := guiRequest(t, server, "POST", "/api/crawl", `{"profile":"missing_profile_xyz"}`)
		if code != 409 || !strings.Contains(body, "not found") {
			t.Fatalf("missing profile = %d %q", code, body)
		}
		code, body = guiRequest(t, server, "POST", "/api/crawl/stop", "")
		if code != 200 || !strings.Contains(body, "stopping") || !app.StopRequested.Load() {
			t.Fatalf("stop = %d %q stopRequested=%v", code, body, app.StopRequested.Load())
		}
	})
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	recorder := httptest.NewRecorder()
	recorder.Body = &buf
	writeJSON(recorder, map[string]int{"ok": 1})
	if recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatal("missing JSON content type")
	}
	if !strings.Contains(buf.String(), `"ok":1`) {
		t.Fatalf("body = %q", buf.String())
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestGUILastProfileFromLog(t *testing.T) {
	withGUIApp(t, func() {
		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(oldCwd)
		if err := os.MkdirAll("logs", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("logs", "LastProfileTest-2026-11-18-Z12-00-00.txt")
		if err := os.WriteFile(path, []byte("session"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/last-profile", "")
		if code != 200 || !strings.Contains(body, "LastProfileTest") {
			t.Fatalf("last-profile = %d %q", code, body)
		}
	})
}

func TestGUIPromptEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "gui_prompt_test.yaml")
		content := "linkOk: |\n  stack|image\nregexRankingKeywords: |\n  (?i)\\W(stack|image)\\W\nbootstrapingLinks:\n- https://example.org/stacking\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/prompt?profile=gui_prompt_test", "")
		if code != 200 {
			t.Fatalf("prompt = %d %q", code, body)
		}
		var payload struct{ Prompt string }
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{"deep and exhaustive investigation", "gui_prompt_test", "https://example.org/stacking", "CURATED CONTENT"} {
			if !strings.Contains(payload.Prompt, fragment) {
				t.Fatalf("prompt missing %q: %q", fragment, payload.Prompt)
			}
		}
	})
}

func TestGUIKeywordsMorphologicalGrouping(t *testing.T) {
	withGUIApp(t, func() {
		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
			t.Fatal(err)
		}
		text := "Astronomical images and image processing require stacked stars and star detection. " +
			" stacking integration calibration dark frames flat frames nebulae galaxies pixels"
		corpus := "https://example.org/a\n" + text + "\n----\n\nhttps://example.org/b\n" + text + "\n----\n"
		if err := os.WriteFile(filepath.Join(dir, "logs", "corpusCuratedText.log"), []byte(corpus), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(oldCwd)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/keywords?profile=anything", "")
		if code != 200 {
			t.Fatalf("keywords = %d %q", code, body)
		}
		var payload struct{ Keywords []string }
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]bool)
		for _, word := range payload.Keywords {
			seen[word] = true
		}
		if seen["stars"] && seen["star"] {
			t.Fatal("star and stars should be grouped into one keyword")
		}
		if !seen["star"] && !seen["stars"] {
			t.Fatal("neither star nor stars was extracted")
		}
	})
}
