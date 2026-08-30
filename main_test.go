package traefik_plugin_cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestMiddleware(next http.Handler, cfg *Config) http.Handler {
	h, _ := New(context.Background(), next, cfg, "test")
	return h
}

func newTestCache(next http.Handler, cfg *Config) *Cache {
	h, _ := New(context.Background(), next, cfg, "test")
	return h.(*Cache)
}

func TestCacheMiss(t *testing.T) {
	cfg := CreateConfig()
	handler := newTestHandler(http.StatusOK, "hello")
	middleware := newTestMiddleware(handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	if rec.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected miss, got %s", rec.Header().Get("X-Cache-Status"))
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got '%s'", rec.Body.String())
	}
}

func TestCacheHit(t *testing.T) {
	cfg := CreateConfig()
	handler := newTestHandler(http.StatusOK, "hello")
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "hit" {
		t.Errorf("expected hit, got %s", rec2.Header().Get("X-Cache-Status"))
	}
	if rec2.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got '%s'", rec2.Body.String())
	}
}

func TestCacheMissDifferentPath(t *testing.T) {
	cfg := CreateConfig()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Path))
	})
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/a", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/b", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected miss for different path, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheBypassMethod(t *testing.T) {
	cfg := CreateConfig()
	handler := newTestHandler(http.StatusOK, "hello")
	middleware := newTestMiddleware(handler, cfg)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	if rec.Header().Get("X-Cache-Status") != "" {
		t.Errorf("expected no cache header for POST, got %s", rec.Header().Get("X-Cache-Status"))
	}
}

func TestCacheExcludesErrors(t *testing.T) {
	cfg := CreateConfig()
	handler := newTestHandler(http.StatusInternalServerError, "error")
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected miss for error response, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheTTL(t *testing.T) {
	cfg := &Config{MaxEntries: 1024, TTLSeconds: 1, AddHeader: true}
	handler := newTestHandler(http.StatusOK, "hello")
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected miss after TTL, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheMaxEntries(t *testing.T) {
	cfg := &Config{MaxEntries: 2, TTLSeconds: 300, AddHeader: true}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	middleware := newTestMiddleware(handler, cfg)

	for _, path := range []string{"/a", "/b", "/c"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}

	for _, path := range []string{"/a", "/b", "/c"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
		if rec.Header().Get("X-Cache-Status") != "miss" {
			t.Errorf("expected miss for %s after eviction, got %s", path, rec.Header().Get("X-Cache-Status"))
		}
	}
}

func TestCacheStatusHeaderDisabled(t *testing.T) {
	cfg := &Config{MaxEntries: 1024, TTLSeconds: 1800, AddHeader: false}
	handler := newTestHandler(http.StatusOK, "hello")
	middleware := newTestMiddleware(handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	if rec.Header().Get("X-Cache-Status") != "" {
		t.Errorf("expected no cache header when disabled, got %s", rec.Header().Get("X-Cache-Status"))
	}
}

func TestCacheHeadersPreserved(t *testing.T) {
	cfg := CreateConfig()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "value")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom 'value', got '%s'", rec2.Header().Get("X-Custom"))
	}
	if rec2.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("expected Content-Type 'text/plain', got '%s'", rec2.Header().Get("Content-Type"))
	}
	if rec2.Header().Get("X-Cache-Status") != "hit" {
		t.Errorf("expected hit, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheMissHeadersNotDuplicated(t *testing.T) {
	cfg := CreateConfig()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "https://ysturasp.ru")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})
	middleware := newTestMiddleware(handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	values := rec.Header().Values("Access-Control-Allow-Origin")
	if len(values) != 1 {
		t.Errorf("expected exactly one Access-Control-Allow-Origin value on a cache miss, got %v", values)
	}
}

func TestCacheKeyVariesByOrigin(t *testing.T) {
	cfg := CreateConfig()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Vary", "Origin")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})
	middleware := newTestMiddleware(handler, cfg)

	reqNoOrigin := httptest.NewRequest(http.MethodGet, "/test", nil)
	recNoOrigin := httptest.NewRecorder()
	middleware.ServeHTTP(recNoOrigin, reqNoOrigin)
	if recNoOrigin.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected no ACAO for a same-origin/no-Origin request, got %q", recNoOrigin.Header().Get("Access-Control-Allow-Origin"))
	}

	reqWithOrigin := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqWithOrigin.Header.Set("Origin", "https://ysturasp.ru")
	recWithOrigin := httptest.NewRecorder()
	middleware.ServeHTTP(recWithOrigin, reqWithOrigin)

	if recWithOrigin.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected a request with a different Origin to be a separate cache miss, got %s", recWithOrigin.Header().Get("X-Cache-Status"))
	}
	if got := recWithOrigin.Header().Get("Access-Control-Allow-Origin"); got != "https://ysturasp.ru" {
		t.Errorf("expected ACAO to reflect the request's Origin, got %q", got)
	}
}

func TestCacheKeyVariesByAcceptEncoding(t *testing.T) {
	cfg := CreateConfig()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch normalizeAcceptEncoding(r.Header.Get("Accept-Encoding")) {
		case "br":
			w.Header().Set("Content-Encoding", "br")
			w.Write([]byte("brotli-body"))
		case "gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write([]byte("gzip-body"))
		default:
			w.Write([]byte("identity-body"))
		}
	})
	middleware := newTestMiddleware(handler, cfg)

	reqBr := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqBr.Header.Set("Accept-Encoding", "br")
	recBr := httptest.NewRecorder()
	middleware.ServeHTTP(recBr, reqBr)
	if recBr.Header().Get("X-Cache-Status") != "miss" {
		t.Fatalf("expected first br request to miss, got %s", recBr.Header().Get("X-Cache-Status"))
	}

	reqNone := httptest.NewRequest(http.MethodGet, "/test", nil)
	recNone := httptest.NewRecorder()
	middleware.ServeHTTP(recNone, reqNone)
	if recNone.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected a request with no Accept-Encoding to be a separate cache miss, not replay the cached br body, got %s", recNone.Header().Get("X-Cache-Status"))
	}
	if recNone.Body.String() != "identity-body" {
		t.Errorf("expected identity body for a request with no Accept-Encoding, got %q", recNone.Body.String())
	}
	if recNone.Header().Get("Content-Encoding") == "br" {
		t.Errorf("client with no Accept-Encoding got a brotli-encoded response it can't decode")
	}

	reqBrAgain := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqBrAgain.Header.Set("Accept-Encoding", "br")
	recBrAgain := httptest.NewRecorder()
	middleware.ServeHTTP(recBrAgain, reqBrAgain)
	if recBrAgain.Header().Get("X-Cache-Status") != "hit" {
		t.Errorf("expected a second br request to hit the first br entry, got %s", recBrAgain.Header().Get("X-Cache-Status"))
	}
	if recBrAgain.Body.String() != "brotli-body" {
		t.Errorf("expected brotli body on br cache hit, got %q", recBrAgain.Body.String())
	}
}

func TestNormalizeAcceptEncoding(t *testing.T) {
	cases := map[string]string{
		"":                              "identity",
		"identity":                      "identity",
		"gzip":                          "gzip",
		"gzip, deflate":                 "gzip",
		"br":                            "br",
		"br;q=1.0, gzip;q=0.8, *;q=0.1": "br",
		"deflate, br":                   "br",
		"DEFLATE, GZIP":                 "gzip",
	}
	for input, want := range cases {
		if got := normalizeAcceptEncoding(input); got != want {
			t.Errorf("normalizeAcceptEncoding(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCacheStatusCodes(t *testing.T) {
	cfg := CreateConfig()

	codes := []int{200, 201, 204, 301}
	for _, code := range codes {
		handler := newTestHandler(code, "body")
		middleware := newTestMiddleware(handler, cfg)

		req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec1 := httptest.NewRecorder()
		middleware.ServeHTTP(rec1, req1)

		req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec2 := httptest.NewRecorder()
		middleware.ServeHTTP(rec2, req2)

		if rec2.Header().Get("X-Cache-Status") != "hit" {
			t.Errorf("expected hit for status %d, got %s", code, rec2.Header().Get("X-Cache-Status"))
		}
	}
}

func TestCache304NeverCached(t *testing.T) {
	cfg := CreateConfig()
	handler := newTestHandler(http.StatusNotModified, "")
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)
	if rec1.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected miss for 304, got %s", rec1.Header().Get("X-Cache-Status"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)
	if rec2.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected a second request to also miss (304 must never be cached), got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheSkipsEntriesOverMaxEntryBytes(t *testing.T) {
	cfg := &Config{MaxEntries: 1024, TTLSeconds: 300, AddHeader: true, MaxEntryBytes: 4}
	handler := newTestHandler(http.StatusOK, "this body is way over the byte cap")
	middleware := newTestMiddleware(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected a response over MaxEntryBytes to never be cached, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheEvictsOldestToStayUnderMaxTotalBytes(t *testing.T) {
	cfg := &Config{MaxEntries: 1024, TTLSeconds: 300, AddHeader: true, MaxEntryBytes: 1024, MaxTotalBytes: 12}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("body" + r.URL.Path[1:]))
	})
	middleware := newTestMiddleware(handler, cfg)

	for _, path := range []string{"/0", "/1", "/2"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/0", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)
	if rec.Header().Get("X-Cache-Status") != "miss" {
		t.Errorf("expected the oldest entry to be evicted once MaxTotalBytes was exceeded, got %s", rec.Header().Get("X-Cache-Status"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/2", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)
	if rec2.Header().Get("X-Cache-Status") != "hit" {
		t.Errorf("expected the most recent entry to still be cached, got %s", rec2.Header().Get("X-Cache-Status"))
	}
}

func TestCacheBypassesWhenOverConcurrentBudget(t *testing.T) {
	cfg := &Config{
		MaxEntries:               1024,
		TTLSeconds:               300,
		AddHeader:                true,
		MaxEntryBytes:            1024,
		MaxConcurrentBufferBytes: 1024,
	}
	handler := newTestHandler(http.StatusOK, "hello")
	c := newTestCache(handler, cfg)

	atomic.StoreInt64(&c.inFlightBufferBytes, 1024)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)

	if rec.Header().Get("X-Cache-Status") != "bypass" {
		t.Errorf("expected bypass when over concurrent budget, got %s", rec.Header().Get("X-Cache-Status"))
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected the request to still succeed with the real body, got %q", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestCacheReleasesConcurrentBudgetAfterRequest(t *testing.T) {
	cfg := &Config{
		MaxEntries:               1024,
		TTLSeconds:               300,
		AddHeader:                true,
		MaxEntryBytes:            1024,
		MaxConcurrentBufferBytes: 4096,
	}
	handler := newTestHandler(http.StatusOK, "hello")
	c := newTestCache(handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)

	if got := atomic.LoadInt64(&c.inFlightBufferBytes); got != 0 {
		t.Errorf("expected in-flight budget released back to 0 after request completes, got %d", got)
	}
}

func TestCacheHitDoesNotConsumeConcurrentBudget(t *testing.T) {
	cfg := &Config{
		MaxEntries:               1024,
		TTLSeconds:               300,
		AddHeader:                true,
		MaxEntryBytes:            1024,
		MaxConcurrentBufferBytes: 4096,
	}
	handler := newTestHandler(http.StatusOK, "hello")
	c := newTestCache(handler, cfg)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	c.ServeHTTP(httptest.NewRecorder(), req1)

	atomic.StoreInt64(&c.inFlightBufferBytes, 4096)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec2 := httptest.NewRecorder()
	c.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Cache-Status") != "hit" {
		t.Errorf("expected a cache hit to succeed regardless of exhausted concurrency budget, got %s", rec2.Header().Get("X-Cache-Status"))
	}
	if rec2.Body.String() != "hello" {
		t.Errorf("expected cached body, got %q", rec2.Body.String())
	}
}

func TestCacheConcurrentBurstNeverExceedsBudget(t *testing.T) {
	const (
		concurrency  = 200
		entryBytes   = 64 * 1024
		budgetBytes  = 512 * 1024
		responseSize = 64 * 1024
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, responseSize))
	})

	cfg := &Config{
		MaxEntries:               10000,
		TTLSeconds:               300,
		AddHeader:                true,
		MaxEntryBytes:            entryBytes,
		MaxTotalBytes:            100 * 1024 * 1024,
		MaxConcurrentBufferBytes: budgetBytes,
	}
	c := newTestCache(handler, cfg)

	var wg sync.WaitGroup
	var maxObserved int64
	var maxMu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/burst-unique-path", nil)
			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, req)

			if got := atomic.LoadInt64(&c.inFlightBufferBytes); got > 0 {
				maxMu.Lock()
				if got > maxObserved {
					maxObserved = got
				}
				maxMu.Unlock()
			}

			if rec.Code != http.StatusOK {
				t.Errorf("request %d: expected 200, got %d", i, rec.Code)
			}
			if rec.Body.Len() != responseSize {
				t.Errorf("request %d: expected full body of %d bytes, got %d", i, responseSize, rec.Body.Len())
			}
		}(i)
	}
	wg.Wait()

	if final := atomic.LoadInt64(&c.inFlightBufferBytes); final != 0 {
		t.Errorf("expected in-flight budget back to 0 after burst, got %d (leaked reservation)", final)
	}
	if maxObserved > budgetBytes {
		t.Errorf("observed in-flight bytes %d exceeded configured budget %d", maxObserved, budgetBytes)
	}
}

func newTestHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}
