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

const interactiveInstructions = `

КОГДА НЕ ЗНАЕШЬ ОТВЕТ (КРИТИЧЕСКИ ВАЖНО):
- Тебе НЕ известны: число гостей/персон, дата, время, и любые предпочтения клиента
  (например, столик у окна или в зале, какой стол, какие блюда). Эти вещи знает только клиент.
- НИКОГДА не называй такие данные наугад: не выдумывай число людей, не выбирай за клиента
  предпочтения. Вместо ответа СРАЗУ вызови инструмент ask_principal с конкретным вопросом.
- Имя, от которого ты звонишь, тебе известно из задачи — его называть можно.
- Вызывай инструмент МОЛЧА: не произноси и не зачитывай вслух ни вызов, ни его аргументы, ни JSON.
- После того как получишь уточнение, продолжи разговор с учётом этой информации.`

// BuildInstructionsInteractive добавляет блок про ask_principal.
func (c *AppConfig) BuildInstructionsInteractive(userContext string) string {
	return c.BuildInstructions(userContext) + interactiveInstructions
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
