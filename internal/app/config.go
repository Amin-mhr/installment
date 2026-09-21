package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"interview/internal/logging"
)

// Environment variables the server reads. .env.example must list every one of them.
const (
	EnvDatabaseURL   = "DATABASE_URL"
	EnvAddr          = "ADDR"
	EnvGinMode       = "GIN_MODE"
	EnvLogDir        = "LOG_DIR"
	EnvLogLevel      = "LOG_LEVEL"
	EnvLogMaxSizeMB  = "LOG_MAX_SIZE_MB"
	EnvLogMaxBackups = "LOG_MAX_BACKUPS"
	EnvLogMaxAgeDays = "LOG_MAX_AGE_DAYS"
	EnvLogCompress   = "LOG_COMPRESS"
)

// EnvNames lists every variable LoadConfig reads.
var EnvNames = []string{
	EnvDatabaseURL, EnvAddr, EnvGinMode,
	EnvLogDir, EnvLogLevel, EnvLogMaxSizeMB, EnvLogMaxBackups, EnvLogMaxAgeDays, EnvLogCompress,
}

type Config struct {
	DatabaseURL string
	Addr        string
	GinMode     string
	Log         logging.Config
}

// LoadConfig reads the configuration from the environment. Files in envFiles
// (default: .env in the working directory) are loaded first when they exist, but
// never override variables that are already set, so real environment variables win.
// Every problem found is reported at once.
func LoadConfig(envFiles ...string) (Config, error) {
	if len(envFiles) == 0 {
		envFiles = []string{".env"}
	}
	for _, f := range envFiles {
		if err := godotenv.Load(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("load %s: %w", f, err)
		}
	}

	var problems []string
	cfg := Config{
		DatabaseURL: os.Getenv(EnvDatabaseURL),
		Addr:        envOr(EnvAddr, ":8080"),
		GinMode:     envOr(EnvGinMode, gin.ReleaseMode),
		Log: logging.Config{
			Dir:        envOr(EnvLogDir, "logs"),
			Level:      envOr(EnvLogLevel, "info"),
			MaxSizeMB:  envPositiveInt(EnvLogMaxSizeMB, 100, &problems),
			MaxBackups: envPositiveInt(EnvLogMaxBackups, 10, &problems),
			MaxAgeDays: envPositiveInt(EnvLogMaxAgeDays, 30, &problems),
			Compress:   envBool(EnvLogCompress, true, &problems),
		},
	}

	if cfg.DatabaseURL == "" {
		problems = append(problems, EnvDatabaseURL+" is required")
	}
	switch cfg.GinMode {
	case gin.DebugMode, gin.ReleaseMode, gin.TestMode:
	default:
		problems = append(problems, fmt.Sprintf("%s must be debug, release or test, got %q", EnvGinMode, cfg.GinMode))
	}
	if _, err := logging.ParseLevel(cfg.Log.Level); err != nil {
		problems = append(problems, EnvLogLevel+": "+err.Error())
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envPositiveInt(key string, fallback int, problems *[]string) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		*problems = append(*problems, fmt.Sprintf("%s must be a positive integer, got %q", key, raw))
		return fallback
	}
	return n
}

func envBool(key string, fallback bool, problems *[]string) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s must be true or false, got %q", key, raw))
		return fallback
	}
	return b
}
