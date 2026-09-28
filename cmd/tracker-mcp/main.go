// Command tracker-mcp runs the Tracker MCP server over stdio or streamable HTTP.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/config"
	"github.com/sarijansharma/tracker-mcp/internal/mcpserver"
	"github.com/sarijansharma/tracker-mcp/internal/repository/postgres"
	"github.com/sarijansharma/tracker-mcp/internal/service"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const shutdownGracePeriod = 10 * time.Second

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "tracker-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	// Logs go to stderr: with the stdio transport, stdout carries the MCP protocol.
	logger := newLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, postgres.PoolOptions{
		MaxConnections: cfg.MaxDBConnections, StatementTimeout: cfg.StatementTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	store := postgres.NewStore(pool)
	actor := service.Actor(cfg.Actor)
	server := mcpserver.NewServer(mcpserver.Dependencies{
		Trackers: service.NewTrackerService(store, actor),
		Columns:  service.NewColumnService(store),
		Rows:     service.NewRowService(store, actor),
		Logger:   logger,
	}, version)

	switch cfg.Transport {
	case config.TransportHTTP:
		return serveHTTP(ctx, cfg, server, logger)
	default:
		logger.Info("tracker-mcp listening on stdio", slog.String("version", version))
		return server.Run(ctx, &mcp.StdioTransport{})
	}
}

func serveHTTP(ctx context.Context, cfg config.Config, server *mcp.Server, logger *slog.Logger) error {
	if !cfg.IsLoopbackOnly() {
		logger.Warn("HTTP transport is reachable from other machines and v1 has NO authentication; "+
			"anyone who can reach it can read and change every tracker. Put it behind an authenticating proxy.",
			slog.String("addr", cfg.HTTPAddress))
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	httpServer := &http.Server{Addr: cfg.HTTPAddress, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("tracker-mcp listening on http", slog.String("url", "http://"+cfg.HTTPAddress+"/mcp"), slog.String("version", version))
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGracePeriod)
		defer cancel()
		logger.Info("shutting down")
		return httpServer.Shutdown(shutdownCtx)
	}
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	if err := slogLevel.UnmarshalText([]byte(level)); err != nil {
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slogLevel}))
}
