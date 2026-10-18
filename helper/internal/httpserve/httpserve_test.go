package httpserve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(t *testing.T, h http.Handler, method, path, auth string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestStateBeforeFirstFrameIs503(t *testing.T) {
	res, _ := get(t, New("").Handler(), "GET", "/state", "")
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") != "1" {
		t.Fatalf("got %d, Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
}

func TestStateServesLatestFrameWithoutNewline(t *testing.T) {
	s := New("")
	s.Broadcast([]byte(`{"v":1,"t":"state","agents":[]}` + "\n"))
	line := []byte(`{"v":1,"t":"state","now":2,"agents":[]}` + "\n")
	s.Broadcast(line)
	line[0] = 'X' // the server must have copied it
	res, body := get(t, s.Handler(), "GET", "/state", "")
	if res.StatusCode != 200 || body != `{"v":1,"t":"state","now":2,"agents":[]}` {
		t.Fatalf("got %d %q", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}

func TestTokenIsRequiredWhenSet(t *testing.T) {
	s := New("secret")
	s.Broadcast([]byte(`{"v":1}`))
	h := s.Handler()
	for _, auth := range []string{"", "Bearer wrong", "secret", "Bearer secret "} {
		if res, _ := get(t, h, "GET", "/state", auth); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: got %d", auth, res.StatusCode)
		}
	}
	if res, _ := get(t, h, "GET", "/state", "Bearer secret"); res.StatusCode != 200 {
		t.Fatalf("right token: got %d", res.StatusCode)
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	res, body := get(t, New("secret").Handler(), "GET", "/healthz", "")
	if res.StatusCode != 200 || body != "ok" {
		t.Fatalf("got %d %q", res.StatusCode, body)
	}
}

func TestOtherMethodsAndPaths(t *testing.T) {
	h := New("").Handler()
	if res, _ := get(t, h, "POST", "/state", ""); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: got %d", res.StatusCode)
	}
	if res, _ := get(t, h, "GET", "/nope", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: got %d", res.StatusCode)
	}
}

func TestCrossOriginReadsAndPreflight(t *testing.T) {
	s := New("secret")
	s.Broadcast([]byte(`{"v":1}`))
	h := s.Handler()
	for _, auth := range []string{"Bearer secret", ""} {
		if res, _ := get(t, h, "GET", "/state", auth); res.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("auth %q: no Access-Control-Allow-Origin on %d", auth, res.StatusCode)
		}
	}
	res, _ := get(t, h, "OPTIONS", "/state", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: got %d", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Headers") != "Authorization" ||
		res.Header.Get("Access-Control-Allow-Methods") != "GET" ||
		res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight headers: %v", res.Header)
	}
}

func TestUsageIsAddedToTheFrame(t *testing.T) {
	s := New("")
	s.Broadcast([]byte(`{"v":1,"t":"state","agents":[]}`))
	s.SetUsage([]byte(`[{"kind":"codex"}]`))
	_, body := get(t, s.Handler(), "GET", "/state", "")
	if body != `{"v":1,"t":"state","agents":[],"usage":[{"kind":"codex"}]}` {
		t.Fatalf("got %q", body)
	}
	_, body = get(t, s.Handler(), "GET", "/state", "")
	if body != `{"v":1,"t":"state","agents":[],"usage":[{"kind":"codex"}]}` {
		t.Fatalf("second answer differs: %q", body)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8787": true, "localhost:8787": true, "[::1]:8787": true,
		":8787": false, "0.0.0.0:8787": false, "192.168.1.20:8787": false, "nonsense": false,
	}
	for addr, want := range cases {
		if got := IsLoopback(addr); got != want {
			t.Errorf("%s: got %v", addr, got)
		}
	}
}

func TestCountIsAlwaysOne(t *testing.T) {
	if New("").Count() != 1 {
		t.Fatal("Count must be 1 so the Runner keeps producing frames")
	}
}
