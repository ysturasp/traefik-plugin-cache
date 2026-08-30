package traefik_plugin_cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Config struct {
	MaxEntries    int  `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
	TTLSeconds    int  `json:"ttlSeconds,omitempty" yaml:"ttlSeconds,omitempty"`
	AddHeader     bool `json:"addHeader,omitempty" yaml:"addHeader,omitempty"`
	Debug         bool `json:"debug,omitempty" yaml:"debug,omitempty"`
	MaxEntryBytes int  `json:"maxEntryBytes,omitempty" yaml:"maxEntryBytes,omitempty"`
	MaxTotalBytes int  `json:"maxTotalBytes,omitempty" yaml:"maxTotalBytes,omitempty"`
}

func CreateConfig() *Config {
	return &Config{
		MaxEntries:    1024,
		TTLSeconds:    1800,
		AddHeader:     true,
		Debug:         false,
		MaxEntryBytes: 512 * 1024,
		MaxTotalBytes: 64 * 1024 * 1024,
	}
}

type cacheEntry struct {
	body       []byte
	header     http.Header
	statusCode int
	createdAt  time.Time
}

type cacheMap struct {
	mu            sync.Mutex
	entries       map[string]*cacheEntry
	max           int
	ttl           time.Duration
	addHeader     bool
	debug         bool
	maxEntryBytes int
	maxTotalBytes int
	totalBytes    int
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
	maxEntryBytes := cfg.MaxEntryBytes
	if maxEntryBytes <= 0 {
		maxEntryBytes = 512 * 1024
	}
	maxTotalBytes := cfg.MaxTotalBytes
	if maxTotalBytes <= 0 {
		maxTotalBytes = 64 * 1024 * 1024
	}
	return &cacheMap{
		entries:       make(map[string]*cacheEntry, maxEntries),
		max:           maxEntries,
		ttl:           ttl,
		addHeader:     cfg.AddHeader,
		debug:         cfg.Debug,
		maxEntryBytes: maxEntryBytes,
		maxTotalBytes: maxTotalBytes,
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
		c.deleteLocked(key, entry)
		return nil, false
	}

	return entry, true
}

func (c *cacheMap) deleteLocked(key string, entry *cacheEntry) {
	delete(c.entries, key)
	c.totalBytes -= len(entry.body)
}

func (c *cacheMap) oldestLocked() (string, *cacheEntry, bool) {
	var oldestKey string
	var oldestEntry *cacheEntry
	for k, v := range c.entries {
		if oldestEntry == nil || v.createdAt.Before(oldestEntry.createdAt) {
			oldestKey = k
			oldestEntry = v
		}
	}
	return oldestKey, oldestEntry, oldestEntry != nil
}

func (c *cacheMap) set(key string, entry *cacheEntry) {
	size := len(entry.body)
	if size > c.maxEntryBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.entries[key]; ok {
		c.deleteLocked(key, existing)
	}

	for len(c.entries) >= c.max || c.totalBytes+size > c.maxTotalBytes {
		oldestKey, oldestEntry, ok := c.oldestLocked()
		if !ok {
			break
		}
		c.deleteLocked(oldestKey, oldestEntry)
	}

	c.entries[key] = entry
	c.totalBytes += size
}

type responseWriter struct {
	http.ResponseWriter
	statusCode  int
	body        *bytes.Buffer
	header      http.Header
	wroteHeader bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
		header:         make(http.Header),
	}
}

func (rw *responseWriter) Header() http.Header {
	return rw.header
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
	next  http.Handler
	name  string
	cache *cacheMap
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

func normalizeAcceptEncoding(raw string) string {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "br") {
		return "br"
	}
	if strings.Contains(lower, "gzip") {
		return "gzip"
	}
	return "identity"
}

func (c *Cache) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		c.next.ServeHTTP(rw, req)
		return
	}

	key := fmt.Sprintf("%s:%s:%s:%s", req.Method, req.URL.RequestURI(), req.Header.Get("Origin"), normalizeAcceptEncoding(req.Header.Get("Accept-Encoding")))
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

	if capture.statusCode >= 200 && capture.statusCode < 400 && capture.statusCode != http.StatusNotModified {
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
