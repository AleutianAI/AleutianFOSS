// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Constants for default connection settings
const (
	DefaultOrchestratorPort = 12210
	DefaultOrchestratorHost = "localhost"
	DefaultTracePort        = 8080
)

// getTraceBaseURL returns the address for the Aleutian Trace server.
// Override with ALEUTIAN_TRACE_URL env var (e.g. for remote or non-default port).
func getTraceBaseURL() string {
	if url := os.Getenv("ALEUTIAN_TRACE_URL"); url != "" {
		return url
	}
	return fmt.Sprintf("http://%s:%d", DefaultOrchestratorHost, DefaultTracePort)
}

// --- Global Variables ---
var (
	requiredSecrets = []SecretDefinition{
		{
			Name:        "aleutian_hf_token",
			DisplayName: "Hugging Face Token",
			Description: "Required for downloading gated models (Llama-3, Pyannote, etc).",
		},
		{
			Name:        "openai_api_key",
			DisplayName: "OpenAI API Key",
			Description: "Required if you select 'openai' as your LLM backend.",
		},
		{
			Name:        "anthropic_api_key",
			DisplayName: "Anthropic API Key",
			Description: "Required if you select 'claude' as your LLM backend.",
		},
		{
			Name:        "google_api_key",
			DisplayName: "Google Gemini API Key",
			Description: "Required if you select 'gemini' as your LLM backend.",
		},
	}
	blockedDirs = map[string]bool{
		".git":          true,
		".venv":         true,
		"node_modules":  true,
		"__pycache__":   true,
		"build":         true,
		"dist":          true,
		".pytest_cache": true,
		".mypy_cache":   true,
	}
	allowedFileExts = map[string]bool{
		".go":   true,
		".py":   true,
		".js":   true,
		".ts":   true,
		".md":   true,
		".txt":  true,
		".java": true,
		".c":    true,
		".cpp":  true,
		".h":    true,
		".hpp":  true,
		".rs":   true,
		".rb":   true,
		".php":  true,
		".html": true,
		".css":  true,
		".json": true,
		".yaml": true,
		".toml": true,
	}
)

type SecretDefinition struct {
	Name        string
	DisplayName string
	Description string
}

// getSystemTotalMemory returns the effective AI Compute Memory in Megabytes.
// On macOS: Returns System RAM (Unified).
// On Linux: Returns NVIDIA VRAM (if GPU present) OR System RAM (fallback).
// This correctly handles "Project DIGITS" (Grace Blackwell) because nvidia-smi
// reports the 128GB Unified Memory as GPU memory on those systems.
func getSystemTotalMemory() (int, error) {
	switch runtime.GOOS {
	case "darwin":
		// macOS: Use sysctl for Unified Memory
		cmd := exec.Command("sysctl", "-n", "hw.memsize")
		out, err := cmd.Output()
		if err != nil {
			return 0, err
		}
		bytesStr := strings.TrimSpace(string(out))
		bytesVal, err := strconv.ParseInt(bytesStr, 10, 64)
		if err != nil {
			return 0, err
		}
		return int(bytesVal / 1024 / 1024), nil // Convert Bytes to MB

	case "linux":
		// 1. Try NVIDIA VRAM first (The AI Priority)
		vram, err := getNvidiaVRAM()
		if err == nil && vram > 0 {
			// On RTX 5090 this returns ~32GB.
			// On Project DIGITS (Grace Blackwell) this returns ~128GB (Unified).
			return vram, nil
		}

		// 2. Fallback to System RAM (No GPU or standard CPU box)
		return getLinuxSystemRAM()

	default:
		// Fallback for Windows/Other (Not officially supported yet)
		return 8192, nil
	}
}

// getNvidiaVRAM sums the memory of all available NVIDIA GPUs using nvidia-smi.
func getNvidiaVRAM() (int, error) {
	// Query memory.total for all GPUs in CSV format
	cmd := exec.Command("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return 0, err // nvidia-smi not found or failed
	}

	totalMemMB := 0
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Output is just the number (e.g., "24576") due to nounits
		memVal, err := strconv.Atoi(line)
		if err == nil {
			totalMemMB += memVal
		}
	}
	return totalMemMB, nil
}

// getLinuxSystemRAM parses /proc/meminfo to find MemTotal.
func getLinuxSystemRAM() (int, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("failed to close /proc/meminfo", "error", err)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Look for line: "MemTotal:       16316372 kB"
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("unexpected format in /proc/meminfo")
			}

			// Value is in kB
			memkB, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}

			return int(memkB / 1024), nil // Convert kB to MB
		}
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}

// getOrchestratorBaseURL returns the standard address for the orchestrator.
func getOrchestratorBaseURL() string {
	// 1. Priority: Environment Variable (Used by Tests & Docker overrides)
	if url := os.Getenv("ALEUTIAN_ORCHESTRATOR_URL"); url != "" {
		return url
	}
	// 2. Default: Standard Host/Port
	return fmt.Sprintf("http://%s:%d", DefaultOrchestratorHost, DefaultOrchestratorPort)
}

func getStackDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get the current working directory %w", err)
	}
	localCompose := filepath.Join(cwd, "podman-compose.yml")
	if _, err = os.Stat(localCompose); err == nil {
		body, err := os.ReadFile(localCompose)
		if err == nil && strings.Contains(string(body), "aleutian-go-orchestrator") {
			return cwd, nil
		}
	}

	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get the current user: %w", err)
	}
	return filepath.Join(usr.HomeDir, ".aleutian", "stack"), nil
}

// ensureStackDir handles version checking, downloading, and updating the stack.
func ensureStackDir(cliVersion string) (string, error) {
	stackDir, err := getStackDir()
	if err != nil {
		return "", err
	}

	// if we are not in the hidden .aleutian directory, do not attempt version check, delete,
	//or download files aka we're in dev mode.
	if !strings.Contains(stackDir, ".aleutian/stack") {
		fmt.Println("Dev Mode detected: using local stack files.")
		fmt.Printf("    Context: %s\n", stackDir)
		if err := ensureEssentialDirs(stackDir); err != nil {
			return "", err
		}
		return stackDir, nil
	}

	if err := ensureEssentialDirs(stackDir); err != nil {
		return "", err
	}

	composeFilePath := filepath.Join(stackDir, "podman-compose.yml")
	versionFilePath := filepath.Join(stackDir, ".version")

	var storedVersion string
	versionBytes, err := os.ReadFile(versionFilePath)
	if err == nil {
		storedVersion = strings.TrimSpace(string(versionBytes))
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Could not read existing stack version file", "path", versionFilePath, "error", err)
	}

	// Dev mode: use current working directory if it has podman-compose.yml
	if cliVersion == "dev" {
		cwd, _ := os.Getwd()
		localCompose := filepath.Join(cwd, "podman-compose.yml")
		if _, err := os.Stat(localCompose); err == nil {
			slog.Info("Dev mode: using local repo", "path", cwd)
			fmt.Printf("Using local stack files from %s\n", cwd)
			return cwd, nil
		}
	}

	composeExists := true
	if _, err := os.Stat(composeFilePath); errors.Is(err, os.ErrNotExist) {
		composeExists = false
	}

	needsUpdate := !composeExists || (storedVersion != cliVersion && cliVersion != "dev")

	if needsUpdate {
		if storedVersion != cliVersion && storedVersion != "" {
			slog.Info("Detected stack version mismatch", "stored", storedVersion, "cli", cliVersion)
			fmt.Printf("Updating stack files in %s to match CLI version %s...\n", stackDir, cliVersion)
		} else {
			slog.Info("Stack files not found", "path", stackDir)
			fmt.Printf("Initializing stack files in %s (v%s)...\n", stackDir, cliVersion)
		}

		dirEntries, _ := os.ReadDir(stackDir)
		for _, entry := range dirEntries {
			name := entry.Name()
			if name == "models" || name == "models_cache" || name == "podman-compose.override.yml" {
				continue
			}
			entryPath := filepath.Join(stackDir, name)
			if err := os.RemoveAll(entryPath); err != nil {
				slog.Warn("Failed to clean up old stack file", "path", entryPath, "error", err)
			}
		}

		if err := downloadAndExtractStackFiles(stackDir, cliVersion); err != nil {
			return "", fmt.Errorf("failed to download stack files: %w", err)
		}

		if err := os.WriteFile(versionFilePath, []byte(cliVersion+"\n"), 0644); err != nil {
			slog.Warn("Failed to write .version file", "error", err)
		}

	} else {
		slog.Info("Using existing stack files", "version", storedVersion)
	}

	return stackDir, nil
}

func ensureEssentialDirs(stackDir string) error {
	dirsToEnsure := []string{"models", "models_cache"}
	var firstErr error
	for _, dirName := range dirsToEnsure {
		dirPath := filepath.Join(stackDir, dirName)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			slog.Info("Creating essential directory", "path", dirPath)
			if err := os.MkdirAll(dirPath, 0755); err != nil {
				slog.Error("Failed to create directory", "path", dirPath, "error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

func ensureSecretExists(def SecretDefinition) bool {
	cmd := exec.Command("podman", "secret", "exists", def.Name)
	if err := cmd.Run(); err == nil {
		return true
	}

	fmt.Printf("\nMissing secret: %s (%s)\n", def.DisplayName, def.Name)
	fmt.Printf("   %s\n", def.Description)
	fmt.Print("   Paste key (or press Enter to skip/leave empty): ")

	reader := bufio.NewReader(os.Stdin)
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	// Use direct command execution with stdin to prevent command injection
	createCmd := exec.Command("podman", "secret", "create", def.Name, "-")
	createCmd.Stdin = strings.NewReader(token)

	if out, err := createCmd.CombinedOutput(); err != nil {
		log.Printf("Failed to create secret %s: %v\nOutput: %s", def.Name, err, string(out))
		return false
	}

	if token == "" {
		fmt.Printf("   Created empty placeholder for %s.\n", def.Name)
	} else {
		fmt.Printf("   %s stored successfully.\n", def.DisplayName)
	}
	return true
}

// ensureAutoGeneratedSecret ensures a secret exists, auto-generating it if missing.
// Used for internal tokens (like InfluxDB) that don't require user input.
// Returns the token value (needed to pass as env var to compose).
func ensureAutoGeneratedSecret(def SecretDefinition) (string, error) {
	// Check if secret already exists
	cmd := exec.Command("podman", "secret", "exists", def.Name)
	if err := cmd.Run(); err == nil {
		// Secret exists - read its value
		inspectCmd := exec.Command("podman", "secret", "inspect", "--showsecret", def.Name)
		out, err := inspectCmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to read existing secret %s: %w", def.Name, err)
		}
		// Parse JSON to extract secret data
		var secrets []struct {
			SecretData string `json:"SecretData"`
		}
		if err := json.Unmarshal(out, &secrets); err != nil {
			return "", fmt.Errorf("failed to parse secret %s: %w", def.Name, err)
		}
		if len(secrets) > 0 && secrets[0].SecretData != "" {
			return secrets[0].SecretData, nil
		}
		return "", fmt.Errorf("secret %s exists but is empty", def.Name)
	}

	// Generate a secure random token (32 bytes = 64 hex chars)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	token := fmt.Sprintf("%x", tokenBytes)

	// Create the secret using direct command execution with stdin to prevent command injection
	createCmd := exec.Command("podman", "secret", "create", def.Name, "-")
	createCmd.Stdin = strings.NewReader(token)
	if out, err := createCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to create secret %s: %v\nOutput: %s", def.Name, err, string(out))
	}

	fmt.Printf("🔐 Auto-generated %s (stored as podman secret)\n", def.DisplayName)
	return token, nil
}

func downloadAndExtractStackFiles(targetDir string, versionTag string) error {
	tarballURL := fmt.Sprintf("https://github.com/AleutianAI/AleutianFOSS/archive/refs/tags/v%s.tar.gz", versionTag)
	slog.Info("Downloading stack archive", "url", tarballURL)
	fmt.Printf("  Downloading %s...\n", tarballURL)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(tarballURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", tarballURL, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Download failed (could not read error body)", "status", resp.StatusCode, "read_error", err)
			return fmt.Errorf("download failed with status %d", resp.StatusCode)
		}
		slog.Error("Download failed", "status", resp.StatusCode)
		return fmt.Errorf("download failed: %s", string(bodyBytes))
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	fmt.Println("  Extracting stack files...")
	return extractTarGz(resp.Body, targetDir)
}

func extractTarGz(gzipStream io.Reader, targetDir string) error {
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		return fmt.Errorf("gzip.NewReader failed: %w", err)
	}
	defer func() {
		if err := uncompressedStream.Close(); err != nil {
			slog.Error("failed to close gzip reader", "error", err)
		}
	}()

	tarReader := tar.NewReader(uncompressedStream)
	var rootDirToStrip string = ""

	processHeader := func(header *tar.Header, reader io.Reader) error {
		if rootDirToStrip == "" {
			if strings.Contains(header.Name, "pax_global_header") || strings.HasPrefix(filepath.Base(header.Name), "._") {
				return nil
			}
			parts := strings.SplitN(header.Name, string(filepath.Separator), 2)
			if len(parts) > 0 && parts[0] != "" {
				if strings.Contains(parts[0], "AleutianLocal") || strings.Contains(parts[0], "AleutianFOSS") {
					rootDirToStrip = parts[0] + string(filepath.Separator)
				} else {
					return fmt.Errorf("could not reliably determine base directory from: '%s'", header.Name)
				}
			} else {
				return fmt.Errorf("unable to determine base directory from: '%s'", header.Name)
			}
		}

		if !strings.HasPrefix(header.Name, rootDirToStrip) {
			return nil
		}

		relPath := strings.TrimPrefix(header.Name, rootDirToStrip)
		if relPath == "" {
			return nil
		}
		relPath = strings.TrimPrefix(relPath, string(filepath.Separator))

		targetPath := filepath.Join(targetDir, relPath)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(targetDir)) {
			return fmt.Errorf("invalid file path: '%s'", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, reader); err != nil {
				if closeErr := outFile.Close(); closeErr != nil {
					slog.Error("failed to close file after copy error", "path", targetPath, "error", closeErr)
				}
				return err
			}
			if err := outFile.Close(); err != nil {
				slog.Error("failed to close extracted file", "path", targetPath, "error", err)
			}
			if err := os.Chmod(targetPath, os.FileMode(header.Mode)); err != nil {
				slog.Error("failed to chmod extracted file", "path", targetPath, "error", err)
			}
		}
		return nil
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := processHeader(header, tarReader); err != nil {
			return err
		}
	}
	return nil
}

func sendPostRequest(url string, payload interface{}) string {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("Error marshaling JSON: %v", err)
	}

	// Use a client with a timeout to prevent tests from hanging
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Sprintf("Error creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// This error usually happens in tests if the URL is unreachable
		return fmt.Sprintf("Error: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body", "error", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("Error reading response: %v", err)
	}

	// Optional: You might want to capture non-200 statuses clearly
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("Error (Status %d): %s", resp.StatusCode, string(body))
	}

	return string(body)
}
