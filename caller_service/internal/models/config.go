package models

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

const instructionsTemplate = `Ты — голосовой ассистент, совершаешь звонок от имени пользователя.
Твоя задача: {context}

Говори ОЧЕНЬ КОРОТКО — максимум 1-2 предложения за раз.
После каждой своей реплики жди ответа собеседника.
Отвечай только на заданный вопрос, не повторяйся.
Говори как обычный человек по телефону.

СТРОГО ЗАПРЕЩЕНО:
- НЕ добавляй в речь технические маркеры, скобки или пометки
- НЕ генерируй реплики за собеседника
- НЕ пиши "Пользователь:", "Ассистент:", "Администратор:" и т.п.
- НЕ придумывай что говорит собеседник
- Отвечай ТОЛЬКО за себя, не имитируй диалог

ЗАВЕРШЕНИЕ РАЗГОВОРА:
- Когда задача выполнена или собеседник не может помочь — попрощайся: "До свидания."
- Система автоматически определит завершение по фразе "До свидания"`

type AppConfig struct {
	APIKey               string
	Folder               string
	HTTPPort             string
	InstructionsTemplate string
}

func (c *AppConfig) BuildInstructions(userContext string) string {
	return strings.ReplaceAll(c.InstructionsTemplate, "{context}", userContext)
}

func LoadConfig() (*AppConfig, error) {
	_ = godotenv.Load()

	apiKey := strings.TrimSpace(os.Getenv("API_KEY"))
	folder := strings.TrimSpace(os.Getenv("FOLDER"))
	httpPort := strings.TrimSpace(os.Getenv("HTTP_PORT"))

	if apiKey == "" || folder == "" {
		return nil, fmt.Errorf("API_KEY и FOLDER должны быть установлены в .env файле")
	}

	if httpPort == "" {
		httpPort = "8080"
	}

	tmpl := strings.TrimSpace(os.Getenv("INSTRUCTIONS_TEMPLATE"))
	if tmpl == "" {
		tmpl = instructionsTemplate
	}

	return &AppConfig{
		APIKey:               apiKey,
		Folder:               folder,
		HTTPPort:             httpPort,
		InstructionsTemplate: tmpl,
	}, nil
}
