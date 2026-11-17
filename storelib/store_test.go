package storelib_test

import (
	"path/filepath"
	"testing"

	"goCrawler/storelib"
)

// TestSQLiteStore verifies SQLite persistence, updates and reopening.
func TestSQLiteStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")

	s, err := storelib.Init(storelib.BackendLocal, path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Exists("k") {
		t.Fatal("clave inexistente reportada como existente")
	}
	if err := s.Set("k", "valor-1"); err != nil {
		t.Fatal(err)
	}
	if !s.Exists("k") {
		t.Fatal("clave no encontrada tras Set")
	}
	if got := s.Get("k"); got != "valor-1" {
		t.Fatalf("Get = %q", got)
	}
	if err := s.Set("k", "valor-2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("k"); got != "valor-2" {
		t.Fatalf("Get tras update = %q", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := storelib.Init(storelib.BackendLocal, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Get("k"); got != "valor-2" {
		t.Fatalf("persistencia tras reabrir: Get = %q", got)
	}
}

// TestUnknownBackend verifies that an invalid backend is rejected.
func TestUnknownBackend(t *testing.T) {
	if _, err := storelib.Init("mysql", "/tmp/opencode/x.db"); err == nil {
		t.Fatal("backend desconocido deberia dar error")
	}
}

// TestRedisStore verifies the Redis backend when a server is available.
func TestRedisStore(t *testing.T) {
	s, err := storelib.Init(storelib.BackendRedis, "")
	if err != nil {
		t.Skipf("Redis no disponible (no debe hacer panic): %v", err)
	}
	defer s.Close()
	if err := s.Set("goCrawler_test_key", "test_value"); err != nil {
		t.Fatal(err)
	}
	if !s.Exists("goCrawler_test_key") {
		t.Fatal("clave no encontrada en Redis")
	}
	if got := s.Get("goCrawler_test_key"); got != "test_value" {
		t.Fatalf("Get = %q", got)
	}
}
