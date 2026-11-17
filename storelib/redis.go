package storelib

import "goCrawler/redislib"

type redisStore struct{}

func newRedisStore() (Store, error) {
	if err := redislib.Ping(); err != nil {
		return nil, err
	}
	return &redisStore{}, nil
}

func (s *redisStore) Name() string                { return "Redis" }
func (s *redisStore) Exists(key string) bool      { return redislib.Exists(key) }
func (s *redisStore) Get(key string) string       { return redislib.Get(key) }
func (s *redisStore) Set(key, value string) error { return redislib.Set(key, value) }
func (s *redisStore) Close() error                { return nil }
