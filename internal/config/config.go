package config

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL        string
	Port               string
	SlackClientID      string
	SlackClientSecret  string
	SlackSigningSecret string
	AnthropicAPIKey    string
	EncryptionKey      string
	BaseURL            string
	RedisURL           string
	SessionSecret      string // HMAC key for PWA session cookies
	Env                string // "dev", "prod" — controls feature flags like dev-login
	WhisperBin         string // path to whisper-cli; empty disables voice transcription
	WhisperModel       string // path to whisper ggml model file
	FFmpegBin          string // path to ffmpeg; defaults to "ffmpeg" on PATH

	// Square OAuth app credentials. Drive the Square integration at
	// internal/apps/square (shift sync today; sales stats tomorrow).
	// Application-level: per-tenant access/refresh tokens live in the
	// integrations row; these app credentials refresh them. Unset values
	// leave the Square connection unavailable rather than crashing.
	SquareApplicationID     string // Square OAuth application_id (client_id)
	SquareApplicationSecret string // Square OAuth application_secret (client_secret)
	SquareEnvironment       string // "production" (default) or "sandbox"

	// Kit GitHub App credentials. The GitHub App is workspace-scoped
	// and shared across every Kit feature that ever touches GitHub
	// (PR-decisions, issue-tasks, and so on).
	// Same rationale as the single shared Slack bot — install once
	// per workspace, used by every feature that needs git/GitHub.
	GitHubAppSlug       string // public slug used in the install URL (https://github.com/apps/<slug>)
	GitHubAppID         int64  // numeric GitHub App ID used to sign installation-token JWTs
	GitHubAppPrivateKey []byte // PEM-encoded RSA private key (read from KIT_GITHUB_APP_PRIVATE_KEY env var or KIT_GITHUB_APP_PRIVATE_KEY_FILE)
}

func Load() (*Config, error) {
	loadDotEnv(".env")

	cfg := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               os.Getenv("PORT"),
		SlackClientID:      os.Getenv("SLACK_CLIENT_ID"),
		SlackClientSecret:  os.Getenv("SLACK_CLIENT_SECRET"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		EncryptionKey:      os.Getenv("ENCRYPTION_KEY"),
		BaseURL:            os.Getenv("BASE_URL"),
		RedisURL:           os.Getenv("REDIS_URL"),
		SessionSecret:      os.Getenv("KIT_SESSION_SECRET"),
		Env:                os.Getenv("KIT_ENV"),
		WhisperBin:         os.Getenv("WHISPER_BIN"),
		WhisperModel:       os.Getenv("WHISPER_MODEL"),
		FFmpegBin:          os.Getenv("FFMPEG_BIN"),
		GitHubAppSlug:      os.Getenv("KIT_GITHUB_APP_SLUG"),

		SquareApplicationID:     os.Getenv("SQUARE_APPLICATION_ID"),
		SquareApplicationSecret: os.Getenv("SQUARE_APPLICATION_SECRET"),
		SquareEnvironment:       os.Getenv("SQUARE_ENVIRONMENT"),
	}

	if cfg.SquareEnvironment == "" {
		cfg.SquareEnvironment = "production"
	}

	if v := os.Getenv("KIT_GITHUB_APP_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.GitHubAppID = n
		}
	}
	if pem := os.Getenv("KIT_GITHUB_APP_PRIVATE_KEY"); pem != "" {
		cfg.GitHubAppPrivateKey = []byte(pem)
	} else if path := os.Getenv("KIT_GITHUB_APP_PRIVATE_KEY_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			cfg.GitHubAppPrivateKey = b
		}
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.FFmpegBin == "" {
		cfg.FFmpegBin = "ffmpeg"
	}

	return cfg, nil
}

// loadDotEnv reads a .env file and sets any vars not already in the environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // missing .env is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Don't override existing env vars
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
