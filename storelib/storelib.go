// Package storelib provides the pluggable key/value store used to cache
// downloaded pages. The local backend (SQLite) needs no external service;
// the Redis backend keeps the original behaviour available.
package storelib

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

const (
	BackendLocal = "local"
	BackendRedis = "redis"
)

var operations atomic.Int64

// Operations returns the number of cache read/write operations performed.
func Operations() int64 { return operations.Load() }

// ResetStats clears the operation counter.
func ResetStats() { operations.Store(0) }

// Store is the minimal interface the crawler needs from its cache.
type Store interface {
	Exists(key string) bool
	Get(key string) string
	Set(key string, value string) error
	Close() error
	Name() string
}

// Init builds the configured store. An unknown backend is an error so a typo
// in the YAML is never silently ignored.
func Init(backend, path string) (Store, error) {
	switch backend {
	case BackendLocal:
		return newSQLiteStore(path)
	case BackendRedis:
		return newRedisStore()
	default:
		return nil, fmt.Errorf("unknown cacheBackend %q (use %q or %q)", backend, BackendLocal, BackendRedis)
	}
}

// ensureDir creates the parent directory of a path if needed.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, os.ModePerm)
}
