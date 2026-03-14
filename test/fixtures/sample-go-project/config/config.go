// Package config loads application configuration for the sample project.
package config

// Config holds all application configuration values.
type Config struct {
	AppName   string
	DSN       string
	SecretKey string
	APIKey    string
}

// Load returns the application configuration.
// NOTE: The hardcoded credential below is intentional — it is a test fixture
// designed to trigger policy check violations for CLI validation testing.
func Load() Config {
	return Config{
		AppName:   "sample-project",
		DSN:       "postgres://localhost:5432/sample",
		SecretKey: "supersecret",
		// FIXTURE: intentional credential pattern for policy check testing.
		APIKey: "sk-test-hardcoded-secret-12345",
	}
}
