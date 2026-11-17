package storelib

import "goCrawler/redislib"

type redisStore struct{}

// newRedisStore opens the Redis backend after checking connectivity.
func newRedisStore() (Store, error) {
	if err := redislib.Ping(); err != nil {
		return nil, err
	}
	return &redisStore{}, nil
}

// Name returns a human-readable backend identifier.
func (s *redisStore) Name() string { return "Redis" }

// Exists reports whether a key is present in Redis.
func (s *redisStore) Exists(key string) bool { return redislib.Exists(key) }

// Get returns the value stored in Redis.
func (s *redisStore) Get(key string) string { return redislib.Get(key) }

// Set stores a value in Redis.
func (s *redisStore) Set(key, value string) error { return redislib.Set(key, value) }

// Close is a no-op because the connection is shared.
func (s *redisStore) Close() error { return nil }
