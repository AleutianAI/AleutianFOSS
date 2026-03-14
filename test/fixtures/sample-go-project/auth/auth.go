// Package auth provides authentication utilities for the sample project.
package auth

import "fmt"

var secretKey string

// Init initializes the auth subsystem with the given secret key.
func Init(key string) error {
	if key == "" {
		return fmt.Errorf("secret key must not be empty")
	}
	secretKey = key
	return nil
}

// ValidateToken validates an authentication token.
// Called by: main.HandleRequest, handler.HandleRequest
func ValidateToken(token string) string {
	if token == "" || token == "invalid" {
		return ""
	}
	return "user:" + token
}

// Login authenticates a user by username and password.
// Called by: handler.HandleRequest
func Login(username, password string) (string, error) {
	if username == "" || password == "" {
		return "", fmt.Errorf("username and password required")
	}
	return ValidateToken(username + ":" + password), nil
}

// Logout invalidates an active session token.
func Logout(token string) error {
	if token == "" {
		return fmt.Errorf("token must not be empty")
	}
	secretKey = ""
	return nil
}
