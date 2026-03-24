package mockauth

import (
	"fmt"
	"os"
	"strings"
	"time"

	"mcpProxy/internal/envfile"
)

type Config struct {
	ListenAddr      string
	Issuer          string
	DefaultUserID   string
	DefaultUserName string
	DefaultScope    string
	ClientID        string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	CodeTTL         time.Duration
	Interactive     bool
	AutoApprove     bool
}

func LoadConfig() (*Config, error) {
	if err := envfile.Load(strings.TrimSpace(os.Getenv("MOCK_AUTH_CONFIG_FILE"))); err != nil {
		return nil, err
	}
	accessTTL, err := durationEnv("MOCK_AUTH_ACCESS_TTL", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	refreshTTL, err := durationEnv("MOCK_AUTH_REFRESH_TTL", 12*time.Hour)
	if err != nil {
		return nil, err
	}
	codeTTL, err := durationEnv("MOCK_AUTH_CODE_TTL", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		ListenAddr:      envOrDefault("MOCK_AUTH_LISTEN_ADDR", "127.0.0.1:18080"),
		Issuer:          envOrDefault("MOCK_AUTH_ISSUER", "http://127.0.0.1:18080"),
		DefaultUserID:   envOrDefault("MOCK_AUTH_DEFAULT_USER_ID", "u12345"),
		DefaultUserName: envOrDefault("MOCK_AUTH_DEFAULT_USER_NAME", "zhangsan"),
		DefaultScope:    envOrDefault("MOCK_AUTH_DEFAULT_SCOPE", "mcp.invoke mcp.read"),
		ClientID:        envOrDefault("MOCK_AUTH_CLIENT_ID", "local-mcp-proxy"),
		AccessTTL:       accessTTL,
		RefreshTTL:      refreshTTL,
		CodeTTL:         codeTTL,
		Interactive:     boolEnv("MOCK_AUTH_INTERACTIVE", false),
		AutoApprove:     boolEnv("MOCK_AUTH_AUTO_APPROVE", true),
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true") || value == "1"
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
