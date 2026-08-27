package traefik_plugin_cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestMiddleware(next http.Handler, cfg *Config) http.Handler {
	h, _ := New(context.Background(), next, cfg, "test")
	return h
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

func TestCacheStatusCodes(t *testing.T) {
	cfg := CreateConfig()

	codes := []int{200, 201, 204, 301, 304}
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

func newTestHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}
