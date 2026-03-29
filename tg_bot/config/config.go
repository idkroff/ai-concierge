package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken         string
	CallerServiceURL string
	YDBDSN           string
	YDBSAKeyFile     string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("BOT_TOKEN is required")
	}

	callerURL := os.Getenv("CALLER_SERVICE_URL")
	if callerURL == "" {
		callerURL = "http://localhost:8080"
	}
	callerURL = strings.TrimRight(callerURL, "/")

	ydbDSN := os.Getenv("YDB_DSN")
	if ydbDSN == "" {
		return nil, fmt.Errorf("YDB_DSN is required")
	}

	ydbSAKeyFile := os.Getenv("YDB_SA_KEY_FILE")
	if ydbSAKeyFile == "" {
		return nil, fmt.Errorf("YDB_SA_KEY_FILE is required")
	}

	return &Config{
		BotToken:         token,
		CallerServiceURL: callerURL,
		YDBDSN:           ydbDSN,
		YDBSAKeyFile:     ydbSAKeyFile,
	}, nil
}
