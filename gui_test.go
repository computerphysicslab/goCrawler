package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"goCrawler/robotspolicy"
	"goCrawler/storelib"
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
			if _, ok := stats["network"].(float64); !ok {
				t.Fatalf("missing network metric: %v", stats)
			}
			if _, ok := stats["cache_io"].(float64); !ok {
				t.Fatalf("missing cache_io metric: %v", stats)
			}
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
		apply := `{"profile":"` + name + `","keywords":["stacking","drizzle"],"removed":["old"]}`
		code, body = guiRequest(t, server, "POST", "/api/keywords/apply", apply)
		if code != 200 || !strings.Contains(body, "applied") {
			t.Fatalf("apply = %d %q", code, body)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(got)
		active := yamlBlockAlternatives(content, "linkOk:")
		for _, word := range active {
			if word == "old" || strings.HasPrefix(word, "old") {
				t.Fatalf("removed keyword is still active in the profile: %q", content)
			}
		}
		if !strings.Contains(content, "excludedKeywords:") {
			t.Fatalf("excluded keywords block missing: %q", content)
		}
		for _, form := range []string{"stack", "stacks", "stacking", "drizzle", "drizzles", "drizzling"} {
			if !strings.Contains(content, form) {
				t.Fatalf("morphological variant %q missing from profile: %q", form, content)
			}
		}
		for _, junk := range []string{"stackinger", "drizzleion", "stackinged"} {
			if strings.Contains(content, junk) {
				t.Fatalf("nonsense variant %q in profile: %q", junk, content)
			}
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

// jsDelimitersBalanced ignores quoted strings and regex literals when checking JS delimiters.
func jsDelimitersBalanced(js string) bool {
	var stack []rune
	var quote rune
	escaped, inRegex, inClass := false, false, false
	prevSignificant := ' '
	for _, r := range js {
		if inRegex {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '[' {
				inClass = true
			} else if r == ']' {
				inClass = false
			} else if r == '/' && !inClass {
				inRegex = false
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '/':
			if strings.ContainsRune("=(,[!:|&?{; ", prevSignificant) {
				inRegex = true
			}
		case '{', '(', '[':
			stack = append(stack, r)
		case '}', ')', ']':
			if len(stack) == 0 {
				return false
			}
			last := stack[len(stack)-1]
			if (r == '}' && last != '{') || (r == ')' && last != '(') || (r == ']' && last != '[') {
				return false
			}
			stack = stack[:len(stack)-1]
		}
		if !unicode.IsSpace(r) {
			prevSignificant = r
		}
	}
	return len(stack) == 0 && quote == 0 && !inRegex
}

func TestGUIScriptIsSyntacticallyBalanced(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/", "")
		if code != 200 {
			t.Fatalf("root = %d", code)
		}
		start := strings.Index(body, "<script>")
		end := strings.Index(body, "</script>")
		if start < 0 || end < start {
			t.Fatal("no script block in GUI HTML")
		}
		js := body[start+len("<script>") : end]
		if !jsDelimitersBalanced(js) {
			t.Fatal("GUI JavaScript has unbalanced delimiters")
		}
		for _, name := range []string{"stats", "profiles", "loadProfile", "saveProfile", "search",
			"extract", "removeKeyword", "applyKeywords", "generatePrompt", "startCrawl", "stopCrawl"} {
			if !strings.Contains(js, "function "+name+"(") && !strings.Contains(js, " "+name+"=") {
				t.Fatalf("GUI JavaScript is missing %s", name)
			}
		}
	})
}

func TestGUIProfilesAndPromptAreSelects(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`<select id="p-list"`, `<select id="pr-profile"`, `id="run-profile"`} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		if !strings.Contains(body, `const match=p.find(x=>last.profile&&x.toLowerCase()===last.profile.toLowerCase())`) {
			t.Fatal("last profile should be selected case-insensitively")
		}
	})
}

func TestGUIPromptUsesFullCuratedTextAndEnglish(t *testing.T) {
	withGUIApp(t, func() {
		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
			t.Fatal(err)
		}
		longText := strings.Repeat("Astronomical image stacking requires careful calibration and registration of every frame. ", 20)
		profile := "linkOk: |\n  stack|image\nregexRankingKeywords: |\n  (?i)\\W(stack|image)\\W\nbootstrapingLinks:\n- https://example.org/stack\n"
		if err := os.WriteFile(filepath.Join(dir, "profiles", "prompt_full_test.yaml"), []byte(profile), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "logs", "corpusCuratedText.log"),
			[]byte("https://example.org/stack\n"+longText+"\n----\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(oldCwd)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/prompt?profile=prompt_full_test", "")
		if code != 200 {
			t.Fatalf("prompt = %d %q", code, body)
		}
		var payload struct{ Prompt string }
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload.Prompt, "[...]") {
			t.Fatal("prompt must not truncate curated text")
		}
		if !strings.Contains(payload.Prompt, strings.TrimSpace(longText)) {
			t.Fatalf("prompt does not contain the complete curated text; prompt tail=%q", payload.Prompt[len(payload.Prompt)-300:])
		}
		if !strings.Contains(payload.Prompt, "must always be written in English") {
			t.Fatal("prompt is missing the English-only requirement")
		}
	})
}

func TestGUIRejectionEventsEndpoint(t *testing.T) {
	withGUIApp(t, func() {
		for i := 0; i < 12; i++ {
			recordRejection("too_short", fmt.Sprintf("https://example.org/%d", i),
				fmt.Sprintf("%d tokens < min 50", i), "short text")
		}
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/rejections?reason=too_short", "")
		if code != 200 {
			t.Fatalf("rejections = %d %q", code, body)
		}
		var payload struct {
			Events []rejectionEvent `json:"events"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Events) != 10 {
			t.Fatalf("got %d events, want 10", len(payload.Events))
		}
		if payload.Events[9].URL != "https://example.org/11" || payload.Events[9].Detail == "" {
			t.Fatalf("latest event wrong: %+v", payload.Events[9])
		}
	})
}

func TestGUIExcludedKeywordsDoNotReappear(t *testing.T) {
	withGUIApp(t, func() {
		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
			t.Fatal(err)
		}
		profile := "linkOk: |\n  stack|image|value\nregexRankingKeywords: |\n  (?i)\\W(stack|image|value)\\W\n"
		if err := os.WriteFile(filepath.Join(dir, "profiles", "exclude_test.yaml"), []byte(profile), 0644); err != nil {
			t.Fatal(err)
		}
		text := "Stacking astronomical images requires calibration and value of every frame. " +
			"Image processing with stacking removes noise and improves the final picture."
		if err := os.WriteFile(filepath.Join(dir, "logs", "corpusCuratedText.log"),
			[]byte("https://example.org/a\n"+text+"\n----\nhttps://example.org/b\n"+text+"\n----\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(oldCwd)

		server := httptest.NewServer(guiHandler())
		defer server.Close()

		_, body := guiRequest(t, server, "GET", "/api/keywords?profile=exclude_test", "")
		var first struct{ Keywords []string }
		if err := json.Unmarshal([]byte(body), &first); err != nil {
			t.Fatal(err)
		}
		foundValue := false
		for _, word := range first.Keywords {
			if word == "value" || word == "values" {
				foundValue = true
			}
		}
		if !foundValue {
			t.Fatalf("value should be present before removal: %v", first.Keywords)
		}

		apply := `{"profile":"exclude_test","keywords":["stack","image"],"removed":["value"]}`
		code, body := guiRequest(t, server, "POST", "/api/keywords/apply", apply)
		if code != 200 {
			t.Fatalf("apply = %d %q", code, body)
		}
		profileAfter, err := os.ReadFile("profiles/exclude_test.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(profileAfter), "excludedKeywords:") || !strings.Contains(string(profileAfter), "value") {
			t.Fatalf("excluded keywords were not persisted: %q", profileAfter)
		}

		_, body = guiRequest(t, server, "GET", "/api/keywords?profile=exclude_test", "")
		var second struct{ Keywords []string }
		if err := json.Unmarshal([]byte(body), &second); err != nil {
			t.Fatal(err)
		}
		for _, word := range second.Keywords {
			if word == "value" || word == "values" {
				t.Fatalf("excluded keyword reappeared: %v", second.Keywords)
			}
		}
	})
}

func TestGUIRejectionRowsAreClickable(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		if !strings.Contains(body, `class=clickable onclick="showReason(\'`) {
			t.Fatal("rejection rows are missing the click handler")
		}
		if !strings.Contains(body, `id="reason-detail"`) {
			t.Fatal("rejection detail container is missing")
		}
	})
}

func TestGUIURLManagerAddRemoveReplace(t *testing.T) {
	withGUIApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "url_manager_test.yaml")
		initial := "scoreThreshold: 10\nlinkOk: |\n  stack\nbootstrapingLinks:\n- \"https://example.org/a\"\n"
		if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()

		code, body := guiRequest(t, server, "GET", "/api/urls?profile=url_manager_test", "")
		if code != 200 || !strings.Contains(body, "https://example.org/a") {
			t.Fatalf("GET urls = %d %q", code, body)
		}

		add := `{"profile":"url_manager_test","urls":["https://example.org/b","not-a-url","https://example.org/b"],"mode":"append"}`
		code, body = guiRequest(t, server, "POST", "/api/urls", add)
		if code != 200 || !strings.Contains(body, `"urls":2`) {
			t.Fatalf("add = %d %q", code, body)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got), "not-a-url") || strings.Count(string(got), "https://example.org/b") != 1 {
			t.Fatalf("invalid or duplicated URL in profile: %q", got)
		}

		remove := `{"profile":"url_manager_test","urls":[],"mode":"append","remove":["https://example.org/a"]}`
		code, body = guiRequest(t, server, "POST", "/api/urls", remove)
		if code != 200 {
			t.Fatalf("remove = %d %q", code, body)
		}
		got, _ = os.ReadFile(path)
		if strings.Contains(string(got), "https://example.org/a") {
			t.Fatalf("removed URL still present: %q", got)
		}

		replace := `{"profile":"url_manager_test","urls":["https://example.org/z"],"mode":"replace"}`
		code, body = guiRequest(t, server, "POST", "/api/urls", replace)
		if code != 200 || !strings.Contains(body, `"urls":1`) {
			t.Fatalf("replace = %d %q", code, body)
		}
		got, _ = os.ReadFile(path)
		if !strings.Contains(string(got), "https://example.org/z") || strings.Contains(string(got), "https://example.org/b") {
			t.Fatalf("replace did not rewrite seed list: %q", got)
		}
	})
}

func TestGUIHasURLTab(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`data-tab="urls"`, `id="urls"`, `id="u-profile"`, `id="u-text"`, "URL Seeds", `id="u-count"`} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		if !jsDelimitersBalanced(strings.Split(strings.Split(body, "<script>")[1], "</script>")[0]) {
			t.Fatal("GUI JavaScript is not balanced after adding hazURL")
		}
	})
}

func TestNetworkAndCacheIOCounters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	withGUIApp(t, func() {
		networkRequests.Store(0)
		robotspolicy.ResetStats()
		storelib.ResetStats()
		app.HTTPClient = &http.Client{Timeout: 2 * time.Second}

		before := networkRequestsTotal()
		resp, err := proxyGet(server.URL+"/page", false)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := networkRequestsTotal(); got <= before {
			t.Fatalf("network counter did not increase: %d -> %d", before, got)
		}

		store, err := storelib.Init(storelib.BackendLocal, filepath.Join(t.TempDir(), "stats.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		beforeIO := storelib.Operations()
		store.Exists("k")
		store.Set("k", "v")
		store.Get("k")
		if got := storelib.Operations(); got < beforeIO+3 {
			t.Fatalf("cache I/O counter did not increase: %d -> %d", beforeIO, got)
		}
	})
}

func TestGUIMetricsInHTML(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`id="s-net"`, `id="s-io"`, "Net reqs", "Cache I/O"} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
	})
}

func TestGUIURLCounterAndDeduplication(t *testing.T) {
	withGUIApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "url_counter_test.yaml")
		initial := "scoreThreshold: 10\nbootstrapingLinks:\n- \"https://example.org/a\"\n"
		if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/api/urls?profile=url_counter_test", "")
		var list struct{ Urls []string }
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Urls) != 1 {
			t.Fatalf("initial count = %d, want 1", len(list.Urls))
		}

		add := `{"profile":"url_counter_test","urls":["https://example.org/a","https://example.org/b","https://example.org/b","bad-url"],"mode":"append"}`
		_, body = guiRequest(t, server, "POST", "/api/urls", add)
		var res struct{ Urls int }
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatal(err)
		}
		if res.Urls != 2 {
			t.Fatalf("count after adding 1 new + 1 duplicate + 1 invalid = %d, want 2", res.Urls)
		}

		add2 := `{"profile":"url_counter_test","urls":["https://example.org/c","https://example.org/d"],"mode":"append"}`
		_, body = guiRequest(t, server, "POST", "/api/urls", add2)
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatal(err)
		}
		if res.Urls != 4 {
			t.Fatalf("count after adding 2 more = %d, want 4", res.Urls)
		}

		_, body = guiRequest(t, server, "GET", "/api/urls?profile=url_counter_test", "")
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Urls) != 4 {
			t.Fatalf("GET after additions = %d, want 4", len(list.Urls))
		}
	})
}

func TestGUISItemsInHTMLAndStats(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{"toggleTheme", "--accent", "@media(max-width:760px)",
			`id="s-rate"`, `id="s-lat"`, `id="s-queue"`, `id="s-goroutines"`, `id="reject-chart"`, "applyAndRun"} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		code, statsBody := guiRequest(t, server, "GET", "/api/stats", "")
		if code != 200 {
			t.Fatalf("stats = %d", code)
		}
		var stats map[string]interface{}
		if err := json.Unmarshal([]byte(statsBody), &stats); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"rate", "avg_latency", "queue", "goroutines"} {
			if _, ok := stats[key].(float64); !ok {
				t.Fatalf("missing numeric metric %q: %v", key, stats)
			}
		}
	})
}

func TestRuntimeKPIHelpers(t *testing.T) {
	withGUIApp(t, func() {
		app.CrawlStartedAt = time.Time{}
		if crawlRate() != 0 {
			t.Fatal("rate should be 0 without a start time")
		}
		app.CrawlStartedAt = time.Now().Add(-2 * time.Second)
		app.CacheHits = 20
		app.OnlineDownloads = 4
		if r := crawlRate(); r < 10 || r > 14 {
			t.Fatalf("crawlRate = %v", r)
		}
		app.OnlineDownloads = 2
		app.DownloadMS = 100
		if got := averageLatency(); got != 50 {
			t.Fatalf("averageLatency = %v", got)
		}
		app.LPool = []aLink{{URL: "https://a.org"}, {URL: "https://b.org"}}
		if got := queueDepth(); got != 2 {
			t.Fatalf("queueDepth = %d", got)
		}
	})
}

func TestGUIURLManagerAcceptsMarkdownLinks(t *testing.T) {
	withGUIApp(t, func() {
		if err := os.MkdirAll("profiles", 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("profiles", "markdown_url_test.yaml")
		if err := os.WriteFile(path, []byte("scoreThreshold: 10\nbootstrapingLinks:\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		payload := `{"profile":"markdown_url_test","mode":"append","urls":["[https://www.astromatic.net/software/sextractor/](https://www.astromatic.net/software/sextractor/)","https://reproject.readthedocs.io/en/latest/"]}`
		code, body := guiRequest(t, server, "POST", "/api/urls", payload)
		if code != 200 || !strings.Contains(body, `"urls":2`) {
			t.Fatalf("markdown URLs rejected: %d %q", code, body)
		}
		_, body = guiRequest(t, server, "GET", "/api/urls?profile=markdown_url_test", "")
		var list struct{ Urls []string }
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Urls) != 2 || !strings.Contains(list.Urls[0], "astromatic.net") || !strings.Contains(list.Urls[1], "reproject") {
			t.Fatalf("unexpected URLs: %v", list.Urls)
		}
	})
}

func TestGUIAboutEndpointAndTab(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()

		code, body := guiRequest(t, server, "GET", "/api/about", "")
		if code != 200 {
			t.Fatalf("about = %d %q", code, body)
		}
		var about map[string]interface{}
		if err := json.Unmarshal([]byte(body), &about); err != nil {
			t.Fatal(err)
		}
		if about["license"] != "MIT" || about["version"] == "" ||
			about["repo"] != "https://github.com/computerphysicslab/goCrawler" ||
			about["linkedin"] != "https://www.linkedin.com/in/semanticwebarchitect/" {
			t.Fatalf("unexpected about payload: %v", about)
		}
		qr, ok := about["qr"].(string)
		if !ok || !strings.HasPrefix(qr, "data:image/png;base64,") {
			t.Fatalf("missing QR data URL: %v", about["qr"])
		}
		if strings.Contains(about["author"].(string), "Pérez") {
			t.Fatal("author must contain only contact details, not the name already in copyright")
		}
		for _, field := range []string{"copyright", "author", "email", "linkedin", "go_version", "platform", "components", "changelog", "roadmap", "issues"} {
			if _, ok := about[field]; !ok {
				t.Fatalf("about is missing %q", field)
			}
		}

		_, html := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`data-tab="about"`, `id="a-qr"`, `id="a-version"`, "loadAbout",
			`about-row`, `about-info`, `about-qr`, `id="a-linkedin"`, `.join(', ')`} {
			if !strings.Contains(html, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		js := strings.Split(strings.Split(html, "<script>")[1], "</script>")[0]
		if !jsDelimitersBalanced(js) {
			t.Fatal("GUI JavaScript is not balanced after adding About tab")
		}
	})
}

func TestRequestTimeoutIsCappedAt30Seconds(t *testing.T) {
	withApp(t, func() {
		if err := yamlInitGeneral(); err != nil {
			t.Fatal(err)
		}
		if app.DownloadTimeout > 30*time.Second || app.DownloadTimeout <= 0 {
			t.Fatalf("download timeout = %v, want <= 30s", app.DownloadTimeout)
		}
	})
}

func TestGUIQueueEndpointAndView(t *testing.T) {
	withGUIApp(t, func() {
		app.LPool = []aLink{
			{URL: "https://example.org/pending", Status: 0},
			{URL: "https://example.org/active", Status: 1},
			{URL: "https://example.org/seed", Status: 4},
		}
		recordQueueEvent("requested", "https://example.org/active")
		recordQueueEvent("retrieved", "https://example.org/done")

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/queue", "")
		if code != 200 {
			t.Fatalf("queue = %d %q", code, body)
		}
		var payload struct {
			Total   int `json:"total"`
			Pending []struct {
				URL   string `json:"url"`
				State string `json:"state"`
			} `json:"pending"`
			Recent []queueEvent `json:"recent"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Total != 3 || len(payload.Pending) != 3 || len(payload.Recent) != 2 {
			t.Fatalf("unexpected queue payload: %s", body)
		}
		states := map[string]string{}
		for _, p := range payload.Pending {
			states[p.URL] = p.State
		}
		if states["https://example.org/active"] != "requested" || states["https://example.org/seed"] != "bootstrap" {
			t.Fatalf("unexpected states: %v", states)
		}

		_, html := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`id="q-total"`, `id="queue-list"`, "loadQueue"} {
			if !strings.Contains(html, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		js := strings.Split(strings.Split(html, "<script>")[1], "</script>")[0]
		if !jsDelimitersBalanced(js) {
			t.Fatal("GUI JavaScript is not balanced after adding queue view")
		}
	})
}

func TestGUIKPILayoutAndWorkerSelector(t *testing.T) {
	withGUIApp(t, func() {
		if newCrawler().CLIWorkers != 4 {
			t.Fatal("default worker count should be 4")
		}
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		_, body := guiRequest(t, server, "GET", "/", "")
		for _, fragment := range []string{`class="kpi-grid"`, `class="kpi-card"`, `id="w-workers"`, `value="4" selected`} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("GUI HTML missing %q", fragment)
			}
		}
		js := strings.Split(strings.Split(body, "<script>")[1], "</script>")[0]
		if !jsDelimitersBalanced(js) {
			t.Fatal("GUI JavaScript is not balanced")
		}
	})
}

func TestGUICrawlEndpointAcceptsWorkerCount(t *testing.T) {
	withGUIApp(t, func() {
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "POST", "/api/crawl",
			`{"profile":"missing_profile_xyz","workers":3}`)
		if code != 404 && code != 409 {
			t.Fatalf("expected profile validation first, got %d %q", code, body)
		}
		if app.CLIWorkers != 3 {
			t.Fatalf("worker count = %d, want 3", app.CLIWorkers)
		}
	})
}

func TestGUICrawlStopSetsState(t *testing.T) {
	withGUIApp(t, func() {
		app.StopRequested.Store(false)
		app.StateDirty = false
		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "POST", "/api/crawl/stop", "")
		if code != 200 || !strings.Contains(body, "stopping") {
			t.Fatalf("stop = %d %q", code, body)
		}
		if !app.StopRequested.Load() || !app.StateDirty {
			t.Fatal("stop did not set cancellation and dirty state")
		}
		_, body = guiRequest(t, server, "GET", "/api/stats", "")
		var stats map[string]interface{}
		if err := json.Unmarshal([]byte(body), &stats); err != nil {
			t.Fatal(err)
		}
		if stats["stopping"] != true {
			t.Fatalf("stats does not reflect stopping: %v", stats)
		}
	})
}

func TestGUIPromptFallsBackToLastProfile(t *testing.T) {
	withGUIApp(t, func() {
		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
			t.Fatal(err)
		}
		profile := "linkOk: |\n  stack|image\nregexRankingKeywords: |\n  (?i)\\W(stack|image)\\W\nbootstrapingLinks:\n- https://example.org/stack\n"
		if err := os.WriteFile(filepath.Join(dir, "profiles", "last_run.yaml"), []byte(profile), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "logs", "LastRun-2026-11-18-Z12-00-00.txt"), []byte("session"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(oldCwd)

		server := httptest.NewServer(guiHandler())
		defer server.Close()
		code, body := guiRequest(t, server, "GET", "/api/prompt", "")
		if code != 200 {
			t.Fatalf("prompt without profile = %d %q", code, body)
		}
		var payload struct{ Prompt string }
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(payload.Prompt), "profile: lastrun") {
			t.Fatalf("prompt did not use the last profile: %q", payload.Prompt)
		}

		_, html := guiRequest(t, server, "GET", "/", "")
		if !strings.Contains(html, "throw new Error") || !strings.Contains(html, "prompt error") {
			t.Fatal("GUI JavaScript lacks visible error handling for prompt generation")
		}
	})
}
