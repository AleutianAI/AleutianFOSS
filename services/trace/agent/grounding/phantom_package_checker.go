// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package grounding

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Constants for PhantomPackageChecker behavior.
const (
	// maxResponseScanLengthPackage limits response scanning for performance.
	maxResponseScanLengthPackage = 15000

	// contextRadiusPackage is the number of characters to include around matches.
	contextRadiusPackage = 50
)

// Package-level compiled regexes for package path extraction (compiled once).
var (
	// goPackagePathPattern matches Go-style package paths.
	// Matches: pkg/config, cmd/orchestrator, internal/utils/helpers
	goPackagePathPattern = regexp.MustCompile(
		`\b(pkg|cmd|internal)/[a-z][a-z0-9_/]*\b`,
	)

	// thePackagePattern matches "the X package" references.
	// Matches: the config package, the utils package
	thePackagePattern = regexp.MustCompile(
		`(?i)the\s+([a-z][a-z0-9_/]+)\s+package`,
	)

	// inPackagePattern matches "in the X package" references.
	// Matches: in the config package, in pkg/utils
	inPackagePattern = regexp.MustCompile(
		`(?i)in\s+(?:the\s+)?([a-z][a-z0-9_/]+)\s+package`,
	)

	// servicesPattern matches services-style paths.
	// Matches: services/trace, services/embeddings
	servicesPattern = regexp.MustCompile(
		`\bservices/[a-z][a-z0-9_/]*\b`,
	)
)

// goStdlibPackages contains Go standard library package names.
// These are always considered valid even if not in KnownPackages.
var goStdlibPackages = map[string]bool{
	// Core packages
	"fmt": true, "os": true, "io": true, "net": true,
	"http": true, "context": true, "sync": true, "time": true,
	"strings": true, "bytes": true, "encoding": true, "crypto": true,
	"reflect": true, "sort": true, "errors": true, "path": true,
	"filepath": true, "bufio": true, "log": true, "regexp": true,
	"strconv": true, "testing": true, "flag": true, "json": true,
	"xml": true, "html": true, "text": true, "math": true,
	"database": true, "sql": true, "archive": true, "compress": true,
	"runtime": true, "debug": true, "unsafe": true, "syscall": true,
	// Sub-packages (base names)
	"atomic": true, "template": true, "base64": true, "hex": true,
	"rand": true, "big": true, "bits": true, "cmplx": true,
	"pprof": true, "trace": true, "scanner": true, "tabwriter": true,
	"heap": true, "list": true, "ring": true, "utf8": true, "utf16": true,
	// Common imports
	"ioutil": true, "exec": true, "signal": true, "user": true,
	"url": true, "rpc": true, "mail": true, "smtp": true,
	"slog": true, "slices": true, "maps": true, "cmp": true,
}

// pythonStdlibPackages contains Python standard library module names.
var pythonStdlibPackages = map[string]bool{
	"os": true, "sys": true, "json": true, "re": true,
	"datetime": true, "collections": true, "itertools": true,
	"functools": true, "typing": true, "pathlib": true,
	"logging": true, "unittest": true, "pytest": true,
	"asyncio": true, "threading": true, "multiprocessing": true,
	"http": true, "urllib": true, "socket": true,
	"hashlib": true, "base64": true, "pickle": true,
	"copy": true, "math": true, "random": true,
	"time": true, "calendar": true, "io": true,
	"abc": true, "contextlib": true, "dataclasses": true,
	"enum": true, "argparse": true, "configparser": true,
	"csv": true, "xml": true, "html": true,
}

// projectLikeNames contains common project package names that suggest conformity hallucination.
// Used to identify when simple package names (without path prefixes) look like
// project packages rather than stdlib packages.
var projectLikeNames = map[string]bool{
	"config": true, "configs": true, "configuration": true,
	"models": true, "model": true,
	"database": true, "db": true,
	"routes": true, "router": true, "routing": true,
	"middleware": true, "middlewares": true,
	"handlers": true, "handler": true,
	"controllers": true, "controller": true,
	"services": true, "service": true,
	"utils": true, "util": true, "utilities": true,
	"helpers": true, "helper": true,
	"common": true, "shared": true,
	"api": true, "apis": true,
	"server": true, "client": true,
	"logger": true, "logging": true,
	"auth": true, "authentication": true,
}

// projectPathPrefixes contains directory prefixes that indicate project paths.
// Paths starting with these prefixes are not considered stdlib packages.
var projectPathPrefixes = []string{
	"pkg/", "cmd/", "internal/", "services/",
	"app/", "src/", "lib/", "test/", "tests/",
}

// packageReference represents an extracted package path from the response.
type packageReference struct {
	// Path is the package path (e.g., "pkg/config").
	Path string

	// Context is the surrounding text for debugging.
	Context string

	// Position is where in the response this reference was found.
	Position int
}

// PhantomPackageChecker detects references to packages that don't exist.
//
// This checker identifies when the LLM references package paths like pkg/config
// or cmd/database that are not present in the codebase. This is distinct from
// PhantomSymbolChecker which validates individual symbols within files.
//
// Thread Safety: Safe for concurrent use (stateless after construction).
type PhantomPackageChecker struct {
	config *PhantomPackageCheckerConfig
}

// NewPhantomPackageChecker creates a new phantom package checker.
//
// Description:
//
//	Creates a checker that detects references to non-existent packages.
//	Uses CheckInput.KnownPackages for validation.
//
// Inputs:
//   - config: Configuration for the checker (nil uses defaults).
//
// Outputs:
//   - *PhantomPackageChecker: The configured checker.
//
// Thread Safety: Safe for concurrent use.
func NewPhantomPackageChecker(config *PhantomPackageCheckerConfig) *PhantomPackageChecker {
	if config == nil {
		config = DefaultPhantomPackageCheckerConfig()
	}

	return &PhantomPackageChecker{
		config: config,
	}
}

// Name implements Checker.
func (c *PhantomPackageChecker) Name() string {
	return "phantom_package_checker"
}

// Check implements Checker.
//
// Description:
//
//	Extracts package path references from the response and validates they exist
//	in KnownPackages or are standard library packages. Non-existent package
//	references are flagged as ViolationPhantomPackage.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - input: The check input containing response and package data. Must not be nil.
//
// Outputs:
//   - []Violation: Any violations found.
//
// Thread Safety: Safe for concurrent use.
func (c *PhantomPackageChecker) Check(ctx context.Context, input *CheckInput) []Violation {
	if !c.config.Enabled {
		return nil
	}

	// Validate input
	if input == nil {
		slog.Warn("phantom_package_checker: input is nil")
		return nil
	}

	// Need package data to validate against
	if input.KnownPackages == nil || len(input.KnownPackages) == 0 {
		slog.Debug("phantom_package_checker: no KnownPackages, skipping validation")
		return nil
	}

	var violations []Violation

	// Limit response size for performance
	response := input.Response
	if len(response) > maxResponseScanLengthPackage {
		response = response[:maxResponseScanLengthPackage]
	}

	// Extract package references from response
	refs := c.extractPackageReferences(response)

	// Early exit if no package references found
	if len(refs) == 0 {
		return nil
	}

	// Limit number of references to check
	if c.config.MaxPackagesToCheck > 0 && len(refs) > c.config.MaxPackagesToCheck {
		refs = refs[:c.config.MaxPackagesToCheck]
	}

	// Check each reference against known packages
	for _, ref := range refs {
		select {
		case <-ctx.Done():
			return violations
		default:
		}

		if !c.packageExists(ref.Path, input) {
			// Build list of available packages for correction
			available := c.getAvailablePackages(input)

			slog.Debug("phantom_package_checker: detected phantom package",
				slog.String("package", ref.Path),
				slog.Int("position", ref.Position),
			)

			violations = append(violations, Violation{
				Type:     ViolationPhantomPackage,
				Severity: SeverityCritical,
				Code:     "PHANTOM_PACKAGE",
				Message:  fmt.Sprintf("Reference to non-existent package '%s'", ref.Path),
				Evidence: ref.Path,
				Expected: "Package should exist in the project",
				Suggestion: fmt.Sprintf(
					"The package '%s' doesn't exist in this codebase. "+
						"Available packages: %s. "+
						"Use code exploration tools to discover actual packages before referencing them.",
					ref.Path, strings.Join(available, ", "),
				),
				LocationOffset: ref.Position,
			})

			// Record metric
			RecordPhantomPackage(ctx, ref.Path)
		}
	}

	return violations
}

// extractPackageReferences extracts package path references from the response.
//
// Description:
//
//	Uses multiple regex patterns to find package references in different
//	contexts (Go-style pkg/cmd/internal, services/, etc.). Deduplicates results
//	by package path to avoid double-counting the same package mentioned multiple times.
//
// Inputs:
//   - response: The LLM response text to scan.
//
// Outputs:
//   - []packageReference: Unique package references found, ordered by position.
//
// Thread Safety: Safe for concurrent use (uses only package-level immutable regexes).
func (c *PhantomPackageChecker) extractPackageReferences(response string) []packageReference {
	seen := make(map[string]bool)
	var refs []packageReference

	addRef := func(path string, pos int, context string) {
		// Skip short paths
		if len(path) < c.config.MinPackageLength {
			return
		}

		// Skip if already seen
		if seen[path] {
			return
		}
		seen[path] = true

		refs = append(refs, packageReference{
			Path:     path,
			Context:  context,
			Position: pos,
		})
	}

	// Extract Go-style package paths (pkg/X, cmd/X, internal/X)
	if c.config.CheckGoPackages {
		matches := goPackagePathPattern.FindAllStringSubmatchIndex(response, -1)
		for _, match := range matches {
			if match[0] >= 0 && match[1] > match[0] {
				path := response[match[0]:match[1]]
				ctx := c.getContext(response, match[0], contextRadiusPackage)
				addRef(path, match[0], ctx)
			}
		}

		// Also check services/ paths
		matches = servicesPattern.FindAllStringSubmatchIndex(response, -1)
		for _, match := range matches {
			if match[0] >= 0 && match[1] > match[0] {
				path := response[match[0]:match[1]]
				ctx := c.getContext(response, match[0], contextRadiusPackage)
				addRef(path, match[0], ctx)
			}
		}
	}

	// Extract "the X package" references
	matches := thePackagePattern.FindAllStringSubmatchIndex(response, -1)
	for _, match := range matches {
		// Group 1 is the package name
		if len(match) >= 4 && match[2] >= 0 && match[3] > match[2] {
			name := response[match[2]:match[3]]
			// Only consider if it looks like a package path (has / or is multi-word)
			if strings.Contains(name, "/") || c.looksLikePackage(name) {
				ctx := c.getContext(response, match[0], contextRadiusPackage)
				addRef(name, match[0], ctx)
			}
		}
	}

	// Extract "in the X package" references
	matches = inPackagePattern.FindAllStringSubmatchIndex(response, -1)
	for _, match := range matches {
		// Group 1 is the package name
		if len(match) >= 4 && match[2] >= 0 && match[3] > match[2] {
			name := response[match[2]:match[3]]
			if strings.Contains(name, "/") || c.looksLikePackage(name) {
				ctx := c.getContext(response, match[0], contextRadiusPackage)
				addRef(name, match[0], ctx)
			}
		}
	}

	return refs
}

// looksLikePackage determines if a name looks like a project package.
//
// Description:
//
//	Returns true for names that look like project packages
//	(config, models, database, routes) vs stdlib (fmt, os).
//	Uses the package-level projectLikeNames map for O(1) lookup.
//
// Inputs:
//   - name: Package name to check.
//
// Outputs:
//   - bool: True if the name looks like a project package.
//
// Thread Safety: Safe for concurrent use (reads from immutable package-level map).
func (c *PhantomPackageChecker) looksLikePackage(name string) bool {
	return projectLikeNames[strings.ToLower(name)]
}

// getContext extracts surrounding text for debugging.
//
// Inputs:
//   - response: The full response text.
//   - position: Character position of the match.
//   - radius: Number of characters to include before and after.
//
// Outputs:
//   - string: Trimmed context string around the position.
//
// Thread Safety: Safe for concurrent use (pure function).
func (c *PhantomPackageChecker) getContext(response string, position, radius int) string {
	start := position - radius
	if start < 0 {
		start = 0
	}
	end := position + radius
	if end > len(response) {
		end = len(response)
	}
	return strings.TrimSpace(response[start:end])
}

// packageExists checks if a package path exists.
//
// Description:
//
//	Checks against KnownPackages first, then stdlib exemptions.
//	Also handles partial matches for nested packages (e.g., "pkg" is valid
//	if "pkg/calcs" exists). Stdlib exemption only applies to paths that
//	look like stdlib (e.g., "fmt", "os") not to project paths like "pkg/database".
//
// Inputs:
//   - path: The package path to check.
//   - input: The check input containing KnownPackages and ProjectLang.
//
// Outputs:
//   - bool: True if the package exists or is exempted.
//
// Thread Safety: Safe for concurrent use (reads from immutable package-level maps).
func (c *PhantomPackageChecker) packageExists(path string, input *CheckInput) bool {
	// Normalize path
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, "/")

	// Check exact match in KnownPackages
	if input.KnownPackages[path] {
		return true
	}

	// Check if it's a parent of a known package
	// e.g., "pkg" is valid if "pkg/calcs" exists
	for known := range input.KnownPackages {
		if strings.HasPrefix(known, path+"/") {
			return true
		}
	}

	// Check stdlib exemptions based on project language
	// Only exempt if the path looks like a stdlib path (no pkg/cmd/internal prefix)
	if c.looksLikeProjectPath(path) {
		// Project paths (pkg/*, cmd/*, internal/*, services/*) are not stdlib
		return false
	}

	baseName := filepath.Base(path)

	// Go stdlib check
	if input.ProjectLang == "go" || input.ProjectLang == "" {
		if goStdlibPackages[baseName] {
			return true
		}
		// Also check full path for net/http style
		if goStdlibPackages[path] {
			return true
		}
	}

	// Python stdlib check
	if input.ProjectLang == "python" || input.ProjectLang == "py" {
		if pythonStdlibPackages[baseName] {
			return true
		}
	}

	return false
}

// looksLikeProjectPath returns true if the path has a project-specific prefix.
//
// Description:
//
//	Checks if the path starts with a common project directory prefix
//	(pkg/, cmd/, internal/, etc.). Such paths should not be exempted
//	by stdlib checks.
//
// Inputs:
//   - path: Package path to check.
//
// Outputs:
//   - bool: True if the path looks like a project path.
//
// Thread Safety: Safe for concurrent use (reads from immutable package-level slice).
func (c *PhantomPackageChecker) looksLikeProjectPath(path string) bool {
	for _, prefix := range projectPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// getAvailablePackages returns sorted list of available packages for correction prompts.
//
// Description:
//
//	Extracts and sorts package paths from KnownPackages. Limits to first 10
//	packages for readability in error messages, appending "..." if truncated.
//
// Inputs:
//   - input: The check input containing KnownPackages.
//
// Outputs:
//   - []string: Sorted list of available package paths.
//
// Thread Safety: Safe for concurrent use (creates new slice).
func (c *PhantomPackageChecker) getAvailablePackages(input *CheckInput) []string {
	var packages []string
	for pkg := range input.KnownPackages {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)

	// Limit to first 10 for readability
	if len(packages) > 10 {
		packages = packages[:10]
		packages = append(packages, "...")
	}

	return packages
}

// DerivePackagesFromFiles extracts unique package paths from file paths.
//
// Description:
//
//	Takes a map of file paths and extracts unique directory paths as packages.
//	Also includes parent packages for nested paths (pkg/api/v1 → pkg/api, pkg).
//
// Inputs:
//   - files: Map of file paths (e.g., "cmd/orchestrator/main.go")
//
// Outputs:
//   - map[string]bool: Unique package paths (e.g., "cmd/orchestrator")
//
// Thread Safety: Safe for concurrent use (pure function).
func DerivePackagesFromFiles(files map[string]bool) map[string]bool {
	packages := make(map[string]bool)

	for filePath := range files {
		// Skip empty paths
		if filePath == "" {
			continue
		}

		// Normalize to forward slashes
		filePath = filepath.ToSlash(filePath)

		// Extract directory path (package path)
		dir := filepath.Dir(filePath)
		if dir == "." || dir == "" {
			continue
		}

		// Add the immediate directory
		packages[dir] = true

		// Also add parent packages for nested paths
		// e.g., "pkg/api/v1" adds "pkg/api/v1", "pkg/api", and "pkg"
		for {
			parent := filepath.Dir(dir)
			if parent == "." || parent == "" || parent == dir {
				break
			}
			packages[parent] = true
			dir = parent
		}
	}

	return packages
}

// RecordPhantomPackage is defined in metrics.go to record phantom package detections.
