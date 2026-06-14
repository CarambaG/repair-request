package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr                string
	DatabaseURL         string
	CookieSecure        bool
	DevSeedUsers        bool
	SessionTTL          time.Duration
	BcryptCost          int
	UploadDir           string
	MaxUploadBytes      int64
	MaxUploadTotalBytes int64
	MaxUploadFiles      int
}

func Load() Config {
	maxUploadBytes := getInt64("MAX_UPLOAD_BYTES", 10<<20)
	if maxUploadBytes <= 0 {
		maxUploadBytes = 10 << 20
	}
	maxUploadTotalBytes := getInt64("MAX_UPLOAD_TOTAL_BYTES", 50<<20)
	if maxUploadTotalBytes < maxUploadBytes {
		maxUploadTotalBytes = maxUploadBytes
	}
	maxUploadFiles := getInt("MAX_UPLOAD_FILES", 5)
	if maxUploadFiles < 1 {
		maxUploadFiles = 1
	}

	return Config{
		Addr:                getEnv("APP_ADDR", ":8080"),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://app:app@localhost:5432/repair_requests?sslmode=disable"),
		CookieSecure:        getBool("COOKIE_SECURE", false),
		DevSeedUsers:        getBool("DEV_SEED_USERS", true),
		SessionTTL:          time.Duration(getInt("SESSION_TTL_HOURS", 168)) * time.Hour,
		BcryptCost:          getInt("BCRYPT_COST", 12),
		UploadDir:           getEnv("UPLOAD_DIR", "uploads"),
		MaxUploadBytes:      maxUploadBytes,
		MaxUploadTotalBytes: maxUploadTotalBytes,
		MaxUploadFiles:      maxUploadFiles,
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
