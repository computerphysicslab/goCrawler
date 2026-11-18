package redislib

import (
	"net"
	"testing"
	"time"
)

func TestNoServerDoesNotPanic(t *testing.T) {
	conn, err := net.DialTimeout("tcp", ":6379", 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Skip("a Redis server is running; skipping the offline-path test")
	}
	if Exists("missing-key") {
		t.Fatal("Exists should return false when Redis is unreachable")
	}
	if got := Get("missing-key"); got != "" {
		t.Fatalf("Get = %q, want empty", got)
	}
	if err := Set("key", "value"); err == nil {
		t.Fatal("Set should return an error when Redis is unreachable")
	}
}

func TestSetInterfaceMarshalling(t *testing.T) {
	payload := []string{"go", "crawler"}
	if err := SetInterface("key", payload); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	if got := GetInterface("key"); got == nil {
		t.Fatal("GetInterface returned nil for a stored value")
	}
}
