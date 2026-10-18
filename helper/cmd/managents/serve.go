package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

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
	goodvibesSource := goodvibes.NewSource(home)
	goodvibesSource.Titles, goodvibesSource.Activity = *titles, *activity
	sources := []detect.Source{
		claudeSource,
		codex.NewSource(home, procs),
		&opencode.Source{Processes: procs, Store: opencode.SQLiteStore{Path: opencode.DefaultDatabasePath(home)}},
		museSource,
		goodvibesSource,
	}
	server := httpserve.New(token)

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
