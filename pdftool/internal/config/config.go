package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config aggregates runtime settings for the PDF tool service.
type Config struct {
	ListenAddr     string
	StorageDir     string
	StaticPrefix   string
	MaxWorkers     int
	OpenAIBaseURL  string
	OpenAIAPIKey   string
	OpenAIModel    string
	RequestTimeout time.Duration
	PDFFontPath    string
}

const (
	defaultListenAddr   = ":8090"
	defaultStorageDir   = "storage/pdf_tool"
	defaultStaticPrefix = "/pdf-data"
	defaultBaseURL      = "https://api.openai.com/v1"
	defaultWorkers      = 4
	defaultTimeoutSec   = 300
)

// Load builds the Config from environment variables.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:    getEnv("PDFTOOL_LISTEN_ADDR", defaultListenAddr),
		StorageDir:    getEnv("PDFTOOL_STORAGE_DIR", defaultStorageDir),
		StaticPrefix:  getEnv("PDFTOOL_STATIC_PREFIX", defaultStaticPrefix),
		OpenAIBaseURL: getEnv("OPENAI_BASE_URL", defaultBaseURL),
		OpenAIAPIKey:  strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		OpenAIModel:   strings.TrimSpace(getEnv("OPENAI_MODEL", os.Getenv("OPENAI_MODEL_ID"))),
		PDFFontPath:   strings.TrimSpace(os.Getenv("PDFTOOL_FONT_PATH")),
	}

	if workersStr := strings.TrimSpace(os.Getenv("PDFTOOL_MAX_WORKERS")); workersStr != "" {
		if v, err := strconv.Atoi(workersStr); err == nil && v > 0 {
			cfg.MaxWorkers = v
		}
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = defaultWorkers
	}

	timeoutStr := strings.TrimSpace(os.Getenv("PDFTOOL_TRANSLATION_TIMEOUT"))
	if timeoutStr == "" {
		cfg.RequestTimeout = time.Duration(defaultTimeoutSec) * time.Second
	} else {
		if seconds, err := strconv.Atoi(timeoutStr); err == nil && seconds > 0 {
			cfg.RequestTimeout = time.Duration(seconds) * time.Second
		} else {
			return Config{}, fmt.Errorf("invalid PDFTOOL_TRANSLATION_TIMEOUT: %q", timeoutStr)
		}
	}

	if !strings.HasPrefix(cfg.StaticPrefix, "/") {
		cfg.StaticPrefix = "/" + cfg.StaticPrefix
	}
	cfg.StorageDir = filepath.Clean(cfg.StorageDir)

	return cfg, nil
}

func getEnv(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val != "" {
		return val
	}
	return fallback
}
