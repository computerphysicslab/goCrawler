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
		if !strings.Contains(body, "/api/keywords/apply") || !strings.Contains(body, "'/api/stats'") {
			t.Fatal("GUI HTML is missing expected API calls")
		}
	})
}

func TestGUIProfilesEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/profiles", "")
		if code != 200 || !strings.Contains(body, "astrophotography") {
			t.Fatalf("profiles = %d %q", code, body)
		}
	})
}

func TestGUIProfileGetAndSave(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()

		code, body := guiRequest(t, server, "GET", "/api/profile?name=astrophotography", "")
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
			stats["error"].(string) != "sample error" {
			t.Fatalf("unexpected stats: %v", stats)
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
		path := filepath.Join("profiles", name+".yaml")
		original := "linkOk: |\n  old\nregexRankingKeywords: |\n  (?i)\\W(old)\\W\n"
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/keywords?profile="+name, "")
		if code != 200 || !strings.Contains(body, "keywords") {
			t.Fatalf("keywords = %d %q", code, body)
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
