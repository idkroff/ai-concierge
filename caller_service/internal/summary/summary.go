package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const yandexLLMURL = "https://llm.api.cloud.yandex.net/foundationModels/v1/completion"

const systemPrompt = `Ты помощник. Тебе дают стенограмму телефонного звонка, который голосовой ассистент совершил по поручению пользователя. Верни пользователю КРАТКИЙ итог звонка.

Ответь СТРОГО в формате, без предисловий:
<status>success|partial|failed</status><summary>1-2 предложения по делу, на русском, без воды и приветствий</summary>

Где status:
- success — задача выполнена / получен нужный ответ;
- partial — частично (что-то выяснили, но не всё, или нужно перезвонить);
- failed — не дозвонились по сути / собеседник не смог помочь / бросили трубку.`

var (
	reStatus  = regexp.MustCompile(`(?is)<status>\s*(success|partial|failed)\s*</status>`)
	reSummary = regexp.MustCompile(`(?is)<summary>\s*(.*?)\s*</summary>`)
)

type llmRequest struct {
	ModelURI          string            `json:"modelUri"`
	CompletionOptions completionOptions `json:"completionOptions"`
	Messages          []llmMessage      `json:"messages"`
}

type completionOptions struct {
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"maxTokens"`
}

type llmMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type llmResponse struct {
	Result struct {
		Alternatives []struct {
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
		} `json:"alternatives"`
	} `json:"result"`
}

var client = &http.Client{Timeout: 12 * time.Second}

// Generate просит YandexGPT кратко резюмировать звонок. Возвращает status
// (success/partial/failed) и текст итога; при любой ошибке — пустые строки.
func Generate(ctx context.Context, apiKey, folder, task, transcript string) (status, summary string, err error) {
	userText := fmt.Sprintf("Задача пользователя: %s\n\nСтенограмма звонка:\n%s", task, transcript)

	reqBody := llmRequest{
		ModelURI: fmt.Sprintf("gpt://%s/yandexgpt-lite/latest", folder),
		CompletionOptions: completionOptions{
			Temperature: 0.3,
			MaxTokens:   200,
		},
		Messages: []llmMessage{
			{Role: "system", Text: systemPrompt},
			{Role: "user", Text: userText},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yandexLLMURL, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("summary request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("summary status %d", resp.StatusCode)
	}

	var llmResp llmResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return "", "", fmt.Errorf("decode summary: %w", err)
	}
	if len(llmResp.Result.Alternatives) == 0 {
		return "", "", fmt.Errorf("summary: no alternatives")
	}

	raw := llmResp.Result.Alternatives[0].Message.Text
	if m := reSummary.FindStringSubmatch(raw); len(m) > 1 {
		summary = strings.TrimSpace(m[1])
	} else {
		// модель не дала теги — берём текст как есть
		summary = strings.TrimSpace(raw)
	}
	if m := reStatus.FindStringSubmatch(raw); len(m) > 1 {
		status = strings.ToLower(m[1])
	}
	return status, summary, nil
}
