// Package httpserve publishes the helper's latest state frame over HTTP, for
// displays that pull from the network instead of listening on USB serial
// (the Roku channel in roku/). The contract is docs/http-api.md.
//
// A Server implements app.Displays, so the existing Runner drives it exactly
// as it drives serial displays: every frame the Runner broadcasts becomes the
// body of GET /state. The Server never detects sessions itself, never blocks
// a request on detection, and serves nothing but the frame it was given.
// GET /state answers 401 for a wrong or missing bearer token (when a token is
// set) and 503 until the first frame arrives. GET /healthz always answers ok.
//
// Every route allows cross-origin reads, and OPTIONS answers CORS preflights,
// so a display that is a web page (the webOS app in webos/) can send its
// bearer token. That grants nothing the token does not already grant: the
// routes are read-only, and no cookies or other ambient credentials exist.
package httpserve

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server holds the latest frame and serves it. The zero value is not usable;
// call New.
type Server struct {
	token string

	mu     sync.RWMutex
	latest []byte
	usage  []byte // JSON array, added to the frame as "usage"

	// Session, when set, answers GET /session?id=<agent id> with that
	// session's recent conversation (any JSON value), or ok false.
	Session func(id string) (any, bool, error)
}

// SetUsage stores subscription usage (a JSON array) to add to every frame
// as its "usage" field; nil leaves it out.
func (s *Server) SetUsage(usage []byte) {
	s.mu.Lock()
	s.usage = usage
	s.mu.Unlock()
}

// New returns a Server. An empty token disables authentication, which the
// serve command only allows on loopback addresses.
func New(token string) *Server {
	return &Server{token: token}
}

// Discover is a no-op: HTTP clients find the server, not the other way round.
func (s *Server) Discover(time.Time) {}

// Count is always 1, so the Runner keeps producing frames whether or not a
// client is polling right now.
func (s *Server) Count() int { return 1 }

// Broadcast stores line (one protocol frame, newline optional) as the body of
// the next GET /state. The caller may reuse line afterwards.
func (s *Server) Broadcast(line []byte) {
	frame := bytes.TrimRight(line, "\r\n")
	stored := make([]byte, len(frame))
	copy(stored, frame)
	s.mu.Lock()
	s.latest = stored
	s.mu.Unlock()
}

// Handler returns the routes described in docs/http-api.md.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("OPTIONS /state", preflight)
	mux.HandleFunc("OPTIONS /healthz", preflight)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /state", s.handleState)
	mux.HandleFunc("OPTIONS /session", preflight)
	mux.HandleFunc("GET /session", s.handleSession)
	return allowCrossOrigin(mux)
}

// allowCrossOrigin lets web pages on other origins (a TV app's file:// page
// has origin "null") read every answer, including 401 and 503, so the page can
// tell a wrong token from an unreachable helper.
func allowCrossOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", "GET")
	h.Set("Access-Control-Allow-Headers", "Authorization")
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="managents"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	frame, usage := s.latest, s.usage
	s.mu.RUnlock()
	if frame == nil {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "no state yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(usage) > 0 && bytes.HasSuffix(frame, []byte("}")) {
		frame = append(append(append(frame[:len(frame)-1:len(frame)-1], `,"usage":`...), usage...), '}')
	}
	_, _ = w.Write(frame)
}

// handleSession serves the read-only session view, when it is enabled. It
// shares /state's token.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="managents"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Session == nil {
		http.Error(w, "session view is off; start serve with --transcripts", http.StatusNotFound)
		return
	}
	body, ok, err := s.Session(r.URL.Query().Get("id"))
	switch {
	case err != nil:
		http.Error(w, "cannot read the session", http.StatusInternalServerError)
		return
	case !ok:
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	want := []byte("Bearer " + s.token)
	got := []byte(r.Header.Get("Authorization"))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// IsLoopback reports whether a listen address such as "127.0.0.1:8787" or
// "localhost:8787" only accepts connections from this computer. An empty host
// (":8787") listens everywhere and is not loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
