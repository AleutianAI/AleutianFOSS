// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package main implements the Aleutian Trace OpenAI-compatible proxy.
//
// Description:
//
//	This binary provides an OpenAI-compatible API endpoint that translates
//	chat completion requests into trace agent loop calls. Any OpenAI-compatible
//	client (Open WebUI, Continue.dev, Cline, Aider) can connect to this proxy
//	and get full CRS reasoning + trace tools transparently.
//
//	The proxy does NOT do its own tool loop. The agent loop handles everything:
//	tool selection (via CRS), tool execution, multi-step reasoning, and response
//	synthesis. The proxy is just a protocol translator.
//
// Usage:
//
//	trace-proxy [flags]
//	  --listen-addr string   Listen address (default: ":12218")
//	  --trace-url string     Trace server URL (default: ALEUTIAN_TRACE_URL env or http://localhost:12217)
//	  --ollama-url string    Ollama URL for /v1/models (default: OLLAMA_URL env or http://localhost:11434)
//	  --project-root string  Default project root (default: current directory, can be set per-request via X-Project-Root header)
//	  --timeout duration     Agent run timeout (default: 5m)
//
// Example:
//
//	# Start proxy with default project root
//	trace-proxy --project-root /path/to/repo
//
//	# Test with curl
//	curl -X POST http://localhost:12218/v1/chat/completions \
//	  -H "Content-Type: application/json" \
//	  -d '{"model": "glm4:latest", "messages": [{"role": "user", "content": "What functions call parseConfig?"}]}'
//
//	# Point Open WebUI at http://localhost:12218 as the API endpoint
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	listenAddr := flag.String("listen-addr", ":12218", "Listen address")
	traceURL := flag.String("trace-url", "", "Trace server URL (default: ALEUTIAN_TRACE_URL env or http://localhost:12217)")
	ollamaURL := flag.String("ollama-url", "", "Ollama URL (default: OLLAMA_URL env or http://localhost:11434)")
	projectRoot := flag.String("project-root", "", "Default project root (optional)")
	timeout := flag.Duration("timeout", 5*time.Minute, "Agent run timeout")
	hostPrefix := flag.String("host-prefix", "", "Host-side path prefix for container path translation (env: TRACE_HOST_PREFIX)")
	containerPrefix := flag.String("container-prefix", "", "Container-side mount point for path translation (env: TRACE_CONTAINER_PREFIX)")
	flag.Parse()

	// Initialize OTel telemetry. AllowDegraded=true so the proxy works
	// even when Jaeger/Prometheus are not running (common for local dev).
	telemetryCfg := telemetry.DefaultConfig()
	telemetryCfg.ServiceName = "aleutian-trace-proxy"
	telemetryCfg.AllowDegraded = true
	telemetryShutdown, telemetryErr := telemetry.Init(context.Background(), telemetryCfg)
	if telemetryErr != nil {
		slog.Warn("Telemetry init failed, running without OTel",
			slog.String("error", telemetryErr.Error()))
	}
	defer func() {
		if telemetryShutdown != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = telemetryShutdown(shutdownCtx)
		}
	}()

	// Resolve URLs: flag > env > default.
	resolvedTraceURL := resolveURL(*traceURL, "ALEUTIAN_TRACE_URL", bridge.DefaultTraceURL)
	resolvedOllamaURL := resolveURL(*ollamaURL, "OLLAMA_URL", "http://localhost:11434")

	// Default project root to current working directory if not set.
	resolvedProjectRoot := *projectRoot
	if resolvedProjectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			resolvedProjectRoot = cwd
			slog.Info("Defaulting project root to current directory",
				slog.String("project_root", cwd))
		}
	}

	resolvedHostPrefix := resolveURL(*hostPrefix, "TRACE_HOST_PREFIX", "")
	resolvedContainerPrefix := resolveURL(*containerPrefix, "TRACE_CONTAINER_PREFIX", "")

	// Auto-derive path translation when project root is set but prefixes
	// are not explicitly configured. The container always mounts projects
	// at /projects (from the compose volume mount), so we can infer both
	// sides of the translation automatically.
	if resolvedProjectRoot != "" && resolvedHostPrefix == "" && resolvedContainerPrefix == "" {
		resolvedHostPrefix = resolvedProjectRoot
		resolvedContainerPrefix = "/projects"
		slog.Info("Auto-derived path translation from project root",
			slog.String("host_prefix", resolvedHostPrefix),
			slog.String("container_prefix", resolvedContainerPrefix))
	}

	config := ProxyConfig{
		ListenAddr:      *listenAddr,
		TraceURL:        resolvedTraceURL,
		OllamaURL:       resolvedOllamaURL,
		ProjectRoot:     resolvedProjectRoot,
		Timeout:         *timeout,
		HostPrefix:      resolvedHostPrefix,
		ContainerPrefix: resolvedContainerPrefix,
	}

	proxy := NewProxyServer(config)

	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux)

	handler := corsMiddleware(loggingMiddleware(mux))

	server := &http.Server{
		Addr:              config.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start session cleanup goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go startSessionCleanup(ctx, proxy)

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutting down proxy server")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", slog.String("error", err.Error()))
		}
	}()

	// CRS-26l: Auto-init on startup so graph is built and symbols are indexed
	// before any user query arrives. Brief delay allows the trace server to
	// finish starting if launched concurrently via stack start.
	if config.ProjectRoot != "" {
		go func() {
			time.Sleep(2 * time.Second)
			if err := proxy.AutoInit(config.ProjectRoot); err != nil {
				slog.Warn("CRS-26l: Auto-init failed, will init on first request",
					slog.String("project_root", config.ProjectRoot),
					slog.String("error", err.Error()))
			} else {
				slog.Info("CRS-26l: Auto-init completed, symbol indexing triggered in background")
				proxy.WatchIndexingProgress(ctx)
			}
		}()
	}

	slog.Info("Starting Aleutian Trace proxy",
		slog.String("version", version),
		slog.String("listen_addr", config.ListenAddr),
		slog.String("trace_url", config.TraceURL),
		slog.String("ollama_url", config.OllamaURL),
		slog.String("project_root", config.ProjectRoot),
		slog.Duration("timeout", config.Timeout),
		slog.String("host_prefix", config.HostPrefix),
		slog.String("container_prefix", config.ContainerPrefix),
	)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// resolveURL resolves a URL from flag value, environment variable, or default.
//
// Description:
//
//	Priority: flag value > environment variable > default value.
//
// Inputs:
//
//	flagValue  - the value from the command-line flag (empty if not set)
//	envKey     - the environment variable name to check
//	defaultVal - the fallback default value
//
// Outputs:
//
//	string - the resolved URL
func resolveURL(flagValue, envKey, defaultVal string) string {
	if flagValue != "" {
		return flagValue
	}
	if envVal := os.Getenv(envKey); envVal != "" {
		return envVal
	}
	return defaultVal
}
