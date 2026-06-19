package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRequiresSecrets(t *testing.T) {
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("BOT_TOKEN_FILE", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing secret error")
	}
}

func TestLoadReadsSecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	botTokenFile := filepath.Join(dir, "bot_token")
	databaseURLFile := filepath.Join(dir, "database_url")
	if err := os.WriteFile(botTokenFile, []byte("token-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseURLFile, []byte("postgres://user:pass@localhost/db?sslmode=disable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOT_TOKEN", "")
	t.Setenv("BOT_TOKEN_FILE", botTokenFile)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", databaseURLFile)
	t.Setenv("BOT_USERNAME", "@planner_bot")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := cfg.BotToken.Value(); got != "token-from-file" {
		t.Fatalf("BotToken = %q", got)
	}
	if got := cfg.DatabaseURL.Value(); got != "postgres://user:pass@localhost/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", got)
	}
	if got := cfg.BotUsername; got != "planner_bot" {
		t.Fatalf("BotUsername = %q", got)
	}
	if got := cfg.BotToken.String(); got != "<redacted>" {
		t.Fatalf("BotToken.String() = %q", got)
	}
}

func TestLoadRejectsDuplicateSecretSources(t *testing.T) {
	dir := t.TempDir()
	botTokenFile := filepath.Join(dir, "bot_token")
	if err := os.WriteFile(botTokenFile, []byte("token-from-file"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOT_TOKEN", "token-from-env")
	t.Setenv("BOT_TOKEN_FILE", botTokenFile)
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db?sslmode=disable")
	t.Setenv("DATABASE_URL_FILE", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want duplicate source error")
	}
}
