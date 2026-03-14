// Package main is the entry point for the sample project.
// This fixture is used by CLI unit and E2E tests for the aleutian CLI.
package main

import (
	"fmt"
	"sample-project/auth"
	"sample-project/config"
	"sample-project/db"
	"sample-project/handler"
)

func main() {
	cfg := config.Load()
	fmt.Printf("Starting with config: %s\n", cfg.AppName)

	conn := db.Connect(cfg.DSN)
	defer db.Close(conn)

	if err := auth.Init(cfg.SecretKey); err != nil {
		fmt.Printf("Auth init failed: %v\n", err)
		return
	}

	handler.StartServer(conn)
}

// HandleRequest is the top-level request dispatcher.
// Called by: handler.StartServer → HandleRequest
func HandleRequest(path string) string {
	token := auth.ValidateToken(path)
	if token == "" {
		return "unauthorized"
	}
	return "ok"
}
