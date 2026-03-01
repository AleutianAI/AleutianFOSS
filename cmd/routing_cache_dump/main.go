// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// routing_cache_dump inspects the Trace agent's routing embedding cache.
//
// The routing cache persists tool embedding vectors (BM25 + embedding hybrid
// router, GR-61) in BadgerDB between service restarts. This tool opens the
// cache read-only and prints a human-readable summary: keys, corpus hashes,
// TTL remaining, per-tool vector dimensions, and a short sample of each
// vector.
//
// Usage:
//
//	routing_cache_dump [--path /path/to/routing/cache]
//
// If --path is not given, reads ROUTING_CACHE_DIR from the environment,
// falling back to ~/.aleutian/cache/routing/.
//
// Exit codes:
//
//	0 — success (including "empty cache" which prints a message and exits 0)
//	1 — error opening or reading the database
package main

import (
	"bytes"
	"encoding/gob"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dgbadger "github.com/dgraph-io/badger/v4"
)

// routerCacheKeyPrefix must match router_cache.go exactly.
const routerCacheKeyPrefix = "routing/emb/v1/"

func main() {
	pathFlag := flag.String("path", "", "Path to routing BadgerDB directory (overrides ROUTING_CACHE_DIR env var)")
	flag.Parse()

	dbPath := *pathFlag
	if dbPath == "" {
		dbPath = os.Getenv("ROUTING_CACHE_DIR")
	}
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fatalf("cannot resolve home directory: %v", err)
		}
		dbPath = filepath.Join(home, ".aleutian", "cache", "routing")
	}

	fmt.Printf("Routing cache path: %s\n", dbPath)

	// Check existence before trying to open — gives a cleaner error message
	// than BadgerDB's "no such file or directory" buried in a long error.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Cache directory does not exist. The service has not yet written any embedding vectors.")
		fmt.Println("Start the Trace service with Ollama running to populate the cache.")
		os.Exit(0)
	}

	// Open read-only. BadgerDB does not have a formal read-only flag in v4,
	// but we only read — no writes are performed.
	opts := dgbadger.DefaultOptions(dbPath).
		WithLogger(nil). // suppress BadgerDB internal logs
		WithReadOnly(true)

	db, err := dgbadger.Open(opts)
	if err != nil {
		fatalf("open BadgerDB at %s: %v", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	// Collect all entries under the routing key prefix.
	type entry struct {
		key        string
		corpusHash string
		expiresAt  time.Time
		hasExpiry  bool
		vectors    map[string][]float32
		rawSize    int
		decodeErr  error
	}

	var entries []entry

	err = db.View(func(txn *dgbadger.Txn) error {
		opts := dgbadger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(routerCacheKeyPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			corpusHash := strings.TrimPrefix(key, routerCacheKeyPrefix)

			var e entry
			e.key = key
			e.corpusHash = corpusHash

			// TTL: item.ExpiresAt() returns Unix seconds, 0 = no expiry.
			if expiresAt := item.ExpiresAt(); expiresAt > 0 {
				e.hasExpiry = true
				e.expiresAt = time.Unix(int64(expiresAt), 0)
			}

			raw, err := item.ValueCopy(nil)
			if err != nil {
				e.decodeErr = fmt.Errorf("copy value: %w", err)
				entries = append(entries, e)
				continue
			}
			e.rawSize = len(raw)

			vectors, err := gobDecode(raw)
			if err != nil {
				e.decodeErr = fmt.Errorf("gob decode: %w", err)
			} else {
				e.vectors = vectors
			}

			entries = append(entries, e)
		}
		return nil
	})
	if err != nil {
		fatalf("read BadgerDB: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("\nNo routing cache entries found.")
		fmt.Println("The service has opened but the embedding warm-up has not completed yet,")
		fmt.Println("or Ollama was unavailable during warm-up.")
		os.Exit(0)
	}

	fmt.Printf("\nFound %d routing cache entr%s:\n", len(entries), plural(len(entries), "y", "ies"))
	fmt.Println(strings.Repeat("─", 80))

	for i, e := range entries {
		fmt.Printf("\n[%d] Key:         %s\n", i+1, e.key)
		fmt.Printf("    Corpus hash: %s\n", e.corpusHash)

		if e.hasExpiry {
			remaining := time.Until(e.expiresAt)
			if remaining < 0 {
				fmt.Printf("    TTL:         EXPIRED (%s ago)\n", (-remaining).Round(time.Second))
			} else {
				fmt.Printf("    TTL:         %s remaining (expires %s)\n",
					remaining.Round(time.Second),
					e.expiresAt.Format("2006-01-02 15:04:05 MST"),
				)
			}
		} else {
			fmt.Printf("    TTL:         no expiry set\n")
		}

		fmt.Printf("    Raw size:    %s\n", formatBytes(e.rawSize))

		if e.decodeErr != nil {
			fmt.Printf("    DECODE ERROR: %v\n", e.decodeErr)
			continue
		}

		fmt.Printf("    Tools:       %d vectors\n", len(e.vectors))

		// Print each tool's vector stats in sorted order.
		toolNames := make([]string, 0, len(e.vectors))
		for name := range e.vectors {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)

		// Determine column width from longest tool name.
		maxNameLen := 0
		for _, n := range toolNames {
			if len(n) > maxNameLen {
				maxNameLen = len(n)
			}
		}
		colWidth := maxNameLen + 2

		fmt.Printf("\n    %-*s  %5s  %7s  %s\n", colWidth, "Tool", "Dims", "L2Norm", "Sample (first 4 values)")
		fmt.Printf("    %s  %s  %s  %s\n",
			strings.Repeat("─", colWidth),
			strings.Repeat("─", 5),
			strings.Repeat("─", 7),
			strings.Repeat("─", 40),
		)

		for _, name := range toolNames {
			vec := e.vectors[name]
			dims := len(vec)
			norm := l2Norm(vec)

			// Print first 4 values for visual inspection.
			sample := formatSample(vec, 4)

			fmt.Printf("    %-*s  %5d  %7.4f  %s\n", colWidth, name, dims, norm, sample)
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 80))
	fmt.Printf("Summary: %d entr%s, cache path: %s\n",
		len(entries), plural(len(entries), "y", "ies"), dbPath)
}

// gobDecode deserializes a map[string][]float32 from gob-encoded bytes.
// Must match router_cache.go exactly.
func gobDecode(data []byte) (map[string][]float32, error) {
	var vectors map[string][]float32
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&vectors); err != nil {
		return nil, err
	}
	return vectors, nil
}

// l2Norm computes the L2 norm of a float32 vector.
// Unit-normalized vectors will show ≈1.0000.
func l2Norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// formatSample returns the first n values of a vector as a bracketed string.
func formatSample(v []float32, n int) string {
	if len(v) == 0 {
		return "[]"
	}
	if n > len(v) {
		n = len(v)
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("%+.4f", v[i])
	}
	suffix := ""
	if len(v) > n {
		suffix = " ..."
	}
	return "[" + strings.Join(parts, ", ") + suffix + "]"
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB (%d bytes)", float64(n)/1024/1024, n)
	case n >= 1024:
		return fmt.Sprintf("%.1f KB (%d bytes)", float64(n)/1024, n)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// plural returns singular or plural suffix based on count.
func plural(n int, singular, pluralSuffix string) string {
	if n == 1 {
		return singular
	}
	return pluralSuffix
}

// fatalf prints to stderr and exits 1.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "routing_cache_dump: "+format+"\n", args...)
	os.Exit(1)
}
