// Package config reads server settings from flags, falling back to environment variables.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Transport selects how MCP clients connect.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)

// Config holds every runtime setting.
type Config struct {
	Transport        Transport
	HTTPAddress      string
	DatabaseURL      string
	Actor            string
	MaxDBConnections int32
	StatementTimeout time.Duration
	LogLevel         string
}

const defaultDatabaseURL = "postgres://localhost:5432/tracker_mcp?sslmode=disable"

// Load parses args (without the program name). Each flag defaults to an environment variable.
func Load(args []string) (Config, error) {
	flags := flag.NewFlagSet("tracker-mcp", flag.ContinueOnError)
	var cfg Config
	transport := flags.String("transport", env("TRACKER_TRANSPORT", string(TransportStdio)), "stdio or http (env TRACKER_TRANSPORT)")
	flags.StringVar(&cfg.HTTPAddress, "http-addr", env("TRACKER_HTTP_ADDR", "127.0.0.1:8080"), "listen address for http transport (env TRACKER_HTTP_ADDR)")
	flags.StringVar(&cfg.DatabaseURL, "database-url", env("DATABASE_URL", defaultDatabaseURL), "Postgres connection URL (env DATABASE_URL)")
	flags.StringVar(&cfg.Actor, "actor", env("TRACKER_ACTOR", ""), "name recorded as created_by on new trackers and rows (env TRACKER_ACTOR)")
	flags.StringVar(&cfg.LogLevel, "log-level", env("TRACKER_LOG_LEVEL", "info"), "debug, info, warn or error (env TRACKER_LOG_LEVEL)")
	maxConns := flags.Int("max-db-connections", envInt("TRACKER_MAX_DB_CONNECTIONS", 10), "database pool size (env TRACKER_MAX_DB_CONNECTIONS)")
	flags.DurationVar(&cfg.StatementTimeout, "statement-timeout", envDuration("TRACKER_STATEMENT_TIMEOUT", 15*time.Second),
		"maximum time a single database statement may run (env TRACKER_STATEMENT_TIMEOUT)")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.Transport = Transport(*transport)
	cfg.MaxDBConnections = int32(min(max(*maxConns, 1), 1000)) //nolint:gosec // clamped to a safe range
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if c.Transport != TransportStdio && c.Transport != TransportHTTP {
		return fmt.Errorf("transport must be %q or %q, got %q", TransportStdio, TransportHTTP, c.Transport)
	}
	if c.DatabaseURL == "" {
		return errors.New("database url is required")
	}
	if c.Transport == TransportHTTP {
		if _, _, err := net.SplitHostPort(c.HTTPAddress); err != nil {
			return fmt.Errorf("invalid http address %q: %w", c.HTTPAddress, err)
		}
	}
	return nil
}

// IsLoopbackOnly reports whether the HTTP listener is reachable only from this machine.
// v1 has no authentication, so listening more widely deserves a loud warning.
func (c Config) IsLoopbackOnly() bool {
	host, _, err := net.SplitHostPort(c.HTTPAddress)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if parsed, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return parsed
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if parsed, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return parsed
	}
	return fallback
}
