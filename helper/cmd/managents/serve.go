package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/app"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/detect/claude"
	"github.com/tonylook/managents/helper/internal/detect/codex"
	"github.com/tonylook/managents/helper/internal/detect/goodvibes"
	"github.com/tonylook/managents/helper/internal/detect/muse"
	"github.com/tonylook/managents/helper/internal/detect/opencode"
	"github.com/tonylook/managents/helper/internal/discovery"
	"github.com/tonylook/managents/helper/internal/httpserve"
	"github.com/tonylook/managents/helper/internal/process"
	"github.com/tonylook/managents/helper/internal/transcript"
	"github.com/tonylook/managents/helper/internal/usage"
)

// serveCommand publishes Claude Code, Codex, OpenCode, Muse Code and
// GoodVibes session state over HTTP for network
// displays such as the Roku channel. See docs/http-api.md.
func serveCommand(ctx context.Context, inv *invocation) error {
	flags := inv.flags()
	addr := flags.String("addr", ":8787",
		"address to listen on; YOUR_LAN_IP:8787 keeps it off other interfaces")
	logLevel := flags.String("log-level", "info", "debug, info, warn or error")
	claudeWindow := claudeContextFlag(flags)
	titles := flags.Bool("titles", true,
		"send each session's title (as the Claude app shows it) along with its folder name")
	activity := flags.Bool("activity", true,
		"send a few words on what each session is doing, or what it needs from you while it waits")
	transcripts := flags.Bool("transcripts", false,
		"serve each session's recent conversation (prompts, replies, tool calls) at GET /session, for a display's read-only view")
	discover := flags.Bool("discovery", true,
		"answer LAN discovery requests that carry this token, on the UDP port with the same number")
	if err := inv.parse(flags); err != nil {
		return err
	}
	token := os.Getenv("MANAGENTS_TOKEN")
	if token == "" && !httpserve.IsLoopback(*addr) {
		return inv.usageError(flags,
			"set MANAGENTS_TOKEN before listening on %s, for example: export MANAGENTS_TOKEN=$(openssl rand -hex 16)", *addr)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	logger, err := newLogger(inv.stderr, *logLevel)
	if err != nil {
		return err
	}

	claudeSource := claude.NewSource(home, process.System{})
	claudeSource.ContextWindow = *claudeWindow
	claudeSource.Titles = *titles
	claudeSource.Activity = *activity
	procs := process.System{}
	museSource := muse.NewSource(home, procs)
	museSource.Titles, museSource.Activity = *titles, *activity
	codexSource := codex.NewSource(home, procs)
	codexSource.Activity = *activity
	goodvibesSource := goodvibes.NewSource(home)
	goodvibesSource.Titles, goodvibesSource.Activity = *titles, *activity
	sources := []detect.Source{
		claudeSource,
		codexSource,
		&opencode.Source{Processes: procs, Store: opencode.SQLiteStore{Path: opencode.DefaultDatabasePath(home)}},
		museSource,
		goodvibesSource,
	}
	server := httpserve.New(token)
	var recorded *sessionRecorder
	if *transcripts {
		recorded = &sessionRecorder{}
		for i, source := range sources {
			sources[i] = recorded.wrap(source)
		}
		server.Session = recorded.view
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()

	runCtx, stopRunner := context.WithCancel(ctx)
	defer stopRunner()
	switch {
	case !*discover || token == "":
	case !discovery.TokenStrongEnough(token):
		logger.Warn("LAN discovery is off: the token is shorter than 32 characters; make one with openssl rand -hex 16")
	default:
		startDiscovery(runCtx, token, listener.Addr(), logger)
	}
	go pollUsage(runCtx, usage.NewPoller(home), server)
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- app.NewRunner(sources, server, logger).Run(runCtx)
	}()
	logger.Info("serving Claude session state", "addr", listener.Addr().String(),
		"auth", token != "", "version", currentVersion())

	var result error
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	case <-ctx.Done():
	}
	stopRunner()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	<-runnerDone
	return result
}

// startDiscovery answers discovery requests (docs/http-api.md#discovery) on
// the UDP port matching the HTTP listener until ctx ends. The socket listens
// on every interface even when the HTTP server does not, because a socket
// bound to one unicast address does not receive broadcasts on every system;
// the reply still names only the HTTP server's own address. Failing to listen
// is logged, not fatal: the display can still use its configured address.
func startDiscovery(ctx context.Context, token string, httpAddr net.Addr, logger *slog.Logger) {
	tcp, ok := httpAddr.(*net.TCPAddr)
	if !ok {
		return
	}
	host := ""
	if !tcp.IP.IsUnspecified() {
		host = tcp.IP.String()
	}
	conn, err := net.ListenPacket("udp4", ":"+strconv.Itoa(tcp.Port))
	if err != nil {
		logger.Warn("LAN discovery is off", "err", err)
		return
	}
	responder := discovery.New(token, host, tcp.Port)
	responder.Logger = logger
	go func() {
		if err := responder.Serve(ctx, conn); err != nil {
			logger.Warn("LAN discovery stopped", "err", err)
		}
	}()
	logger.Debug("answering LAN discovery", "port", tcp.Port)
}

// pollUsage refreshes the subscription usage served with every frame.
func pollUsage(ctx context.Context, poller *usage.Poller, server *httpserve.Server) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if data, err := json.Marshal(poller.Snapshot(ctx, time.Now())); err == nil {
			server.SetUsage(data)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sessionRecorder remembers, from each detection, which log file belongs to
// which session id, so GET /session can read it without detecting again.
type sessionRecorder struct {
	mu    sync.Mutex
	bySrc map[string]map[string]agent.Session // source name -> id -> session
}

type recordingSource struct {
	detect.Source
	r *sessionRecorder
}

func (r *sessionRecorder) wrap(s detect.Source) detect.Source { return recordingSource{s, r} }

func (s recordingSource) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	found, err := s.Source.Sessions(ctx, now)
	byID := make(map[string]agent.Session, len(found))
	for _, session := range found {
		byID[session.ID] = session
	}
	s.r.mu.Lock()
	if s.r.bySrc == nil {
		s.r.bySrc = map[string]map[string]agent.Session{}
	}
	s.r.bySrc[s.Name()] = byID
	s.r.mu.Unlock()
	return found, err
}

// sessionView is the body of GET /session.
type sessionView struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	Title    string               `json:"title,omitempty"`
	Dir      string               `json:"name"`
	Messages []transcript.Message `json:"messages"`
}

func (r *sessionRecorder) view(id string) (any, bool, error) {
	r.mu.Lock()
	var session agent.Session
	found := false
	for _, byID := range r.bySrc {
		if s, ok := byID[id]; ok {
			session, found = s, true
		}
	}
	r.mu.Unlock()
	if !found || session.Transcript == "" {
		return nil, false, nil
	}
	messages, err := transcript.Read(string(session.Kind), session.Transcript)
	if errors.Is(err, transcript.ErrUnknownKind) || errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return sessionView{ID: id, Kind: string(session.Kind), Title: session.Title, Dir: filepath.Base(session.Dir), Messages: messages}, true, nil
}
