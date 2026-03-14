// Package handler contains HTTP request handling for the sample project.
package handler

import (
	"fmt"
	"sample-project/auth"
	"sample-project/db"
)

// Conn is a placeholder for a database connection handle.
type Conn = db.Conn

// StartServer starts the HTTP server and listens for incoming requests.
// Called by: main.main
func StartServer(conn Conn) {
	fmt.Println("Server started")
	HandleRequest(conn, "/ping", "test-token")
}

// HandleRequest processes a single HTTP request.
// Called by: StartServer
func HandleRequest(conn Conn, path, token string) string {
	user := auth.ValidateToken(token)
	if user == "" {
		return "unauthorized"
	}

	result, err := db.Query(conn, "SELECT 1")
	if err != nil {
		return "db error"
	}

	return fmt.Sprintf("ok: %s %s", path, result)
}
