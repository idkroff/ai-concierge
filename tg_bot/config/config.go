package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken         string
	CallerServiceURL string
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

	return &Config{
		BotToken:         token,
		CallerServiceURL: callerURL,
	}, nil
}
