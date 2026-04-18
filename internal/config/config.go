package config

import (
	"bufio"
	"errors"
	"os"
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
