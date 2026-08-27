package traefik_plugin_cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	MaxEntries int    `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
	TTLSeconds int    `json:"ttlSeconds,omitempty" yaml:"ttlSeconds,omitempty"`
	AddHeader  bool   `json:"addHeader,omitempty" yaml:"addHeader,omitempty"`
	Debug      bool   `json:"debug,omitempty" yaml:"debug,omitempty"`
}

func CreateConfig() *Config {
	return &Config{
		MaxEntries: 1024,
		TTLSeconds: 1800,
		AddHeader:  true,
		Debug:      false,
	}
}

type cacheEntry struct {
	body       []byte
	header     http.Header
	statusCode int
	createdAt  time.Time
}

type cacheMap struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	max      int
	ttl      time.Duration
	addHeader bool
	debug    bool
}

func newCacheMap(cfg *Config) *cacheMap {
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	ttl := time.Duration(cfg.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 1800 * time.Second
	}
	return &cacheMap{
		entries:   make(map[string]*cacheEntry, maxEntries),
		max:       maxEntries,
		ttl:       ttl,
		addHeader: cfg.AddHeader,
		debug:     cfg.Debug,
	}
}

func (c *cacheMap) get(key string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Since(entry.createdAt) > c.ttl {
		delete(c.entries, key)
		return nil, false
	}

	return entry, true
}

func (c *cacheMap) set(key string, entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.max {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, v := range c.entries {
			if first || v.createdAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.createdAt
				first = false
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}

	c.entries[key] = entry
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	wroteHeader bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.wroteHeader = true
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.body.Write(b)
}

type Cache struct {
	next   http.Handler
	name   string
	cache  *cacheMap
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if config == nil {
		config = CreateConfig()
	}
	return &Cache{
		next:  next,
		name:  name,
		cache: newCacheMap(config),
	}, nil
}

func (c *Cache) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		c.next.ServeHTTP(rw, req)
		return
	}

	key := fmt.Sprintf("%s:%s", req.Method, req.URL.RequestURI())
	hash := sha256.Sum256([]byte(key))
	keyHex := fmt.Sprintf("%x", hash)

	if entry, ok := c.cache.get(keyHex); ok {
		if c.cache.debug {
			rw.Header().Set("X-Cache-Debug", "hit")
		}
		if c.cache.addHeader {
			rw.Header().Set("X-Cache-Status", "hit")
		}
		for k, vv := range entry.header {
			for _, v := range vv {
				rw.Header().Add(k, v)
			}
		}
		rw.WriteHeader(entry.statusCode)
		rw.Write(entry.body)
		return
	}

	capture := newResponseWriter(rw)
	c.next.ServeHTTP(capture, req)

	if capture.statusCode >= 200 && capture.statusCode < 400 {
		entry := &cacheEntry{
			body:       capture.body.Bytes(),
			header:     capture.Header().Clone(),
			statusCode: capture.statusCode,
			createdAt:  time.Now(),
		}
		c.cache.set(keyHex, entry)
	}

	if c.cache.addHeader {
		rw.Header().Set("X-Cache-Status", "miss")
	}

	for k, vv := range capture.Header() {
		for _, v := range vv {
			rw.Header().Add(k, v)
		}
	}
	rw.WriteHeader(capture.statusCode)
	rw.Write(capture.body.Bytes())
}
