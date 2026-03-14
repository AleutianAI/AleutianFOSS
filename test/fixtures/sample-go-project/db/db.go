// Package db provides database connectivity for the sample project.
package db

import "fmt"

// Conn represents a database connection handle.
type Conn struct {
	DSN    string
	active bool
}

// Connect opens a new database connection.
// Called by: main.main
func Connect(dsn string) Conn {
	return Conn{DSN: dsn, active: true}
}

// Close releases a database connection.
// Called by: main.main
func Close(conn Conn) {
	if !conn.active {
		return
	}
	fmt.Printf("Closing connection to %s\n", conn.DSN)
}

// Query executes a SQL query and returns a result string.
// Called by: handler.HandleRequest
func Query(conn Conn, sql string) (string, error) {
	if !conn.active {
		return "", fmt.Errorf("connection is not active")
	}
	if sql == "" {
		return "", fmt.Errorf("sql must not be empty")
	}
	return fmt.Sprintf("result:%s", sql), nil
}
