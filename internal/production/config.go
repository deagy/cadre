package production

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all production configuration.
type Config struct {
	// Server
	Port            int
	Host            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration

	// Logging
	LogLevel  string
	LogFormat string // json or text
	LogOutput string // stdout or file path

	// Cache
	CacheEnabled bool
	CacheSize    int
	CacheTTL     time.Duration

	// Rate Limiting
	RateLimitEnabled bool
	RateLimitRPS     float64
	QuotaWindow      time.Duration

	// Agents
	MaxAgents    int
	AgentTimeout time.Duration

	// Security
	RequireAuth bool
	APIKeyEnv   string

	// Health
	HealthCheckPath string
	ReadyCheckPath  string

	// Environment
	Environment string // development, staging, production
	Version     string
}

// NewConfigFromEnv loads configuration from environment variables.
func NewConfigFromEnv() *Config {
	return &Config{
		// Server
		Port:            getEnvInt("PORT", 8080),
		Host:            getEnvString("HOST", "0.0.0.0"),
		ReadTimeout:     getEnvDuration("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:    getEnvDuration("WRITE_TIMEOUT", 30*time.Second),
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),

		// Logging
		LogLevel:  getEnvString("LOG_LEVEL", "info"),
		LogFormat: getEnvString("LOG_FORMAT", "json"),
		LogOutput: getEnvString("LOG_OUTPUT", "stdout"),

		// Cache
		CacheEnabled: getEnvBool("CACHE_ENABLED", true),
		CacheSize:    getEnvInt("CACHE_SIZE", 1000),
		CacheTTL:     getEnvDuration("CACHE_TTL", 1*time.Hour),

		// Rate Limiting
		RateLimitEnabled: getEnvBool("RATE_LIMIT_ENABLED", true),
		RateLimitRPS:     getEnvFloat("RATE_LIMIT_RPS", 100.0),
		QuotaWindow:      getEnvDuration("QUOTA_WINDOW", 1*time.Minute),

		// Agents
		MaxAgents:    getEnvInt("MAX_AGENTS", 10),
		AgentTimeout: getEnvDuration("AGENT_TIMEOUT", 5*time.Minute),

		// Security
		RequireAuth: getEnvBool("REQUIRE_AUTH", false),
		APIKeyEnv:   getEnvString("API_KEY_ENV", "API_KEY"),

		// Health
		HealthCheckPath: getEnvString("HEALTH_CHECK_PATH", "/health"),
		ReadyCheckPath:  getEnvString("READY_CHECK_PATH", "/ready"),

		// Environment
		Environment: getEnvString("ENVIRONMENT", "development"),
		Version:     getEnvString("VERSION", "unknown"),
	}
}

// Validate checks if configuration is valid.
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}

	if c.CacheSize < 0 {
		return fmt.Errorf("invalid cache size: %d", c.CacheSize)
	}

	if c.RateLimitRPS < 0 {
		return fmt.Errorf("invalid rate limit RPS: %f", c.RateLimitRPS)
	}

	if c.MaxAgents < 1 {
		return fmt.Errorf("invalid max agents: %d", c.MaxAgents)
	}

	validEnvs := map[string]bool{
		"development": true,
		"staging":     true,
		"production":  true,
	}

	if !validEnvs[c.Environment] {
		return fmt.Errorf("invalid environment: %s", c.Environment)
	}

	return nil
}

// Helper functions

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
