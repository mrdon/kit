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

	// Netlify OAuth app credentials. Drive the Netlify website-
	// management app at internal/apps/netlify. Unset values leave the
	// corresponding Connect button disabled in the PWA settings page
	// rather than crashing — local dev paths don't need either
	// credential to bring the rest of Kit up.
	NetlifyClientID     string // Netlify OAuth app client_id
	NetlifyClientSecret string // Netlify OAuth app client_secret

	// Kit GitHub App credentials. The GitHub App is workspace-scoped
	// and shared across every Kit feature that ever touches GitHub
	// (today: netlify; tomorrow: PR-decisions, issue-tasks, etc.).
	// Same rationale as the single shared Slack bot — install once
	// per workspace, used by every feature that needs git/GitHub.
	GitHubAppSlug       string // public slug used in the install URL (https://github.com/apps/<slug>)
	GitHubAppID         int64  // numeric GitHub App ID used to sign installation-token JWTs
	GitHubAppPrivateKey []byte // PEM-encoded RSA private key (read from KIT_GITHUB_APP_PRIVATE_KEY env var or KIT_GITHUB_APP_PRIVATE_KEY_FILE)
}

func Load() (*Config, error) {
	loadDotEnv(".env")

	cfg := &Config{
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		Port:                os.Getenv("PORT"),
		SlackClientID:       os.Getenv("SLACK_CLIENT_ID"),
		SlackClientSecret:   os.Getenv("SLACK_CLIENT_SECRET"),
		SlackSigningSecret:  os.Getenv("SLACK_SIGNING_SECRET"),
		AnthropicAPIKey:     os.Getenv("ANTHROPIC_API_KEY"),
		EncryptionKey:       os.Getenv("ENCRYPTION_KEY"),
		BaseURL:             os.Getenv("BASE_URL"),
		RedisURL:            os.Getenv("REDIS_URL"),
		SessionSecret:       os.Getenv("KIT_SESSION_SECRET"),
		Env:                 os.Getenv("KIT_ENV"),
		WhisperBin:          os.Getenv("WHISPER_BIN"),
		WhisperModel:        os.Getenv("WHISPER_MODEL"),
		FFmpegBin:           os.Getenv("FFMPEG_BIN"),
		NetlifyClientID:     os.Getenv("NETLIFY_CLIENT_ID"),
		NetlifyClientSecret: os.Getenv("NETLIFY_CLIENT_SECRET"),
		GitHubAppSlug:       os.Getenv("KIT_GITHUB_APP_SLUG"),
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
