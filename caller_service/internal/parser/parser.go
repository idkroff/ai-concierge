package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	yandexLLMURL = "https://llm.api.cloud.yandex.net/foundationModels/v1/completion"

	systemPrompt = `<instructions>
Извлеки из сообщения пользователя номер телефона и цель звонка.

Правила нормализации номера:
- Верни ровно 11 цифр без пробелов, скобок, тире и других символов
- Если номер начинается с 8 — замени первую цифру на 7
- Если номер не найден — оставь phone_number пустым

Отвечай ТОЛЬКО тегами phone_number и context, без предисловий и пояснений.
</instructions>

<examples>
  <example>
    <input>Позвони по номеру +7 (495) 739-00-33 и забронируй столик на двоих</input>
    <output><phone_number>74957390033</phone_number><context>забронируй столик на двоих</context></output>
  </example>
  <example>
    <input>8 800 555 35 35 спроси есть ли в наличии аспирин</input>
    <output><phone_number>78005553535</phone_number><context>спроси есть ли в наличии аспирин</context></output>
  </example>
  <example>
    <input>позвони 89161234567 и уточни время работы</input>
    <output><phone_number>79161234567</phone_number><context>уточни время работы</context></output>
  </example>
  <example>
    <input>узнай расписание поездов</input>
    <output><phone_number></phone_number><context>узнай расписание поездов</context></output>
  </example>
</examples>`
)

var digitsOnly = regexp.MustCompile(`\D`)

// llmParsedFlexible — YandexGPT иногда оборачивает ответ в <output>, иногда кладёт теги в корень;
// иногда добавляет markdown ``` вокруг XML.
type llmParsedFlexible struct {
	PhoneDirect string `xml:"phone_number"`
	CtxDirect   string `xml:"context"`
	Output      struct {
		Phone string `xml:"phone_number"`
		Ctx   string `xml:"context"`
	} `xml:"output"`
}

func (f llmParsedFlexible) phoneAndContext() (phone, ctx string) {
	p, c := strings.TrimSpace(f.Output.Phone), strings.TrimSpace(f.Output.Ctx)
	if p != "" || c != "" {
		return p, c
	}
	return strings.TrimSpace(f.PhoneDirect), strings.TrimSpace(f.CtxDirect)
}

var rePhoneTag = regexp.MustCompile(`(?i)<phone_number>\s*([^<]*?)\s*</phone_number>`)
var reCtxTag = regexp.MustCompile(`(?i)<context>\s*([^<]*?)\s*</context>`)

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		first := strings.ToLower(strings.TrimSpace(s[:nl]))
		if first == "xml" || first == "html" {
			s = strings.TrimSpace(s[nl+1:])
		}
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func extractTagsFallback(s string) (phone, ctx string) {
	if m := rePhoneTag.FindStringSubmatch(s); len(m) > 1 {
		phone = strings.TrimSpace(m[1])
	}
	if m := reCtxTag.FindStringSubmatch(s); len(m) > 1 {
		ctx = strings.TrimSpace(m[1])
	}
	return phone, ctx
}

type Result struct {
	PhoneNumber string // 11 цифр без пробелов, например 79991234567
	Context     string // цель звонка без номера
}

type Parser struct {
	apiKey   string
	folderID string
	client   *http.Client
}

func New(apiKey, folderID string) *Parser {
	return &Parser{
		apiKey:   apiKey,
		folderID: folderID,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

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

func (p *Parser) Parse(ctx context.Context, message string) (*Result, error) {
	reqBody := llmRequest{
		ModelURI: fmt.Sprintf("gpt://%s/yandexgpt-lite/latest", p.folderID),
		CompletionOptions: completionOptions{
			Temperature: 0,
			MaxTokens:   150,
		},
		Messages: []llmMessage{
			{Role: "system", Text: systemPrompt},
			{Role: "user", Text: "<input>" + message + "</input>"},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yandexLLMURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from LLM: %d", resp.StatusCode)
	}

	var llmResp llmResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(llmResp.Result.Alternatives) == 0 {
		return nil, fmt.Errorf("LLM returned no alternatives")
	}

	rawText := stripMarkdownFences(llmResp.Result.Alternatives[0].Message.Text)

	var flex llmParsedFlexible
	rawPhone, rawCtx := "", ""
	if err := xml.Unmarshal([]byte("<root>"+rawText+"</root>"), &flex); err == nil {
		rawPhone, rawCtx = flex.phoneAndContext()
	}
	if rawPhone == "" {
		rawPhone, rawCtx = extractTagsFallback(rawText)
	}
	if rawPhone == "" {
		return nil, fmt.Errorf("не удалось извлечь номер телефона из сообщения")
	}

	digits := digitsOnly.ReplaceAllString(rawPhone, "")
	if len(digits) != 11 {
		return nil, fmt.Errorf("извлечённый номер имеет неверный формат: %q", rawPhone)
	}

	return &Result{
		PhoneNumber: digits,
		Context:     rawCtx,
	}, nil
}
