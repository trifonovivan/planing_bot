package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken        Secret
	DatabaseURL     Secret
	AppEnv          string
	DefaultTimezone string
	DigestTime      string
	MetricsEnabled  bool
	MetricsAddr     string
}

type Secret struct {
	name  string
	value string
}

func (s Secret) Value() string {
	return s.value
}

func (s Secret) String() string {
	if s.value == "" {
		return "<empty>"
	}
	return "<redacted>"
}

func Load() (Config, error) {
	botToken, err := loadRequiredSecret("BOT_TOKEN")
	if err != nil {
		return Config{}, err
	}
	databaseURL, err := loadRequiredSecret("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	return Config{
		BotToken:        botToken,
		DatabaseURL:     databaseURL,
		AppEnv:          getEnv("APP_ENV", "local"),
		DefaultTimezone: getEnv("DEFAULT_TIMEZONE", "Europe/Moscow"),
		DigestTime:      getEnv("DIGEST_TIME", "09:30"),
		MetricsEnabled:  getBoolEnv("METRICS_ENABLED", true),
		MetricsAddr:     getEnv("METRICS_ADDR", ":8080"),
	}, nil
}

func (c Config) Location() (*time.Location, error) {
	loc, err := time.LoadLocation(c.DefaultTimezone)
	if err != nil {
		return nil, fmt.Errorf("load default timezone %q: %w", c.DefaultTimezone, err)
	}
	return loc, nil
}

func (c Config) DigestClock() (hour int, minute int, err error) {
	t, err := time.Parse("15:04", c.DigestTime)
	if err != nil {
		return 0, 0, fmt.Errorf("parse DIGEST_TIME %q: %w", c.DigestTime, err)
	}
	return t.Hour(), t.Minute(), nil
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func loadRequiredSecret(name string) (Secret, error) {
	value := strings.TrimSpace(os.Getenv(name))
	filePath := strings.TrimSpace(os.Getenv(name + "_FILE"))

	if value != "" && filePath != "" {
		return Secret{}, fmt.Errorf("%s and %s_FILE are both set; use only one source", name, name)
	}
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return Secret{}, fmt.Errorf("read %s_FILE: %w", name, err)
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" {
		return Secret{}, fmt.Errorf("%s is required; set %s or %s_FILE", name, name, name)
	}
	return Secret{name: name, value: value}, nil
}
