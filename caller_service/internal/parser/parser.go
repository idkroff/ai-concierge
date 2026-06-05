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

	"concierge/internal/orgsearch"
)

const (
	yandexLLMURL = "https://llm.api.cloud.yandex.net/foundationModels/v1/completion"

	systemPrompt = `<instructions>
Извлеки из сообщения пользователя: номер телефона, цель звонка и (если телефон НЕ указан явно) название организации/места.

Правила:
- Если в сообщении есть номер телефона — верни его в phone_number: ровно 11 цифр без пробелов, скобок и тире; если начинается с 8 — замени первую цифру на 7. organization оставь пустым.
- Если телефона НЕТ, но указано название организации/заведения (возможно с уточнением района, улицы или города) — оставь phone_number пустым, а в organization положи это название вместе с уточнением местоположения, как написал пользователь.
- В context всегда помещай цель звонка (без номера и без названия организации).
- Если нет ни номера, ни названия организации — оставь phone_number и organization пустыми.

Отвечай ТОЛЬКО тегами phone_number, organization и context, без предисловий и пояснений.
</instructions>

<examples>
  <example>
    <input>Позвони по номеру +7 (495) 739-00-33 и забронируй столик на двоих</input>
    <output><phone_number>74957390033</phone_number><organization></organization><context>забронируй столик на двоих</context></output>
  </example>
  <example>
    <input>тануки на таганской забронируй столик на 19:00</input>
    <output><phone_number></phone_number><organization>тануки на таганской</organization><context>забронируй столик на 19:00</context></output>
  </example>
  <example>
    <input>8 800 555 35 35 спроси есть ли в наличии аспирин</input>
    <output><phone_number>78005553535</phone_number><organization></organization><context>спроси есть ли в наличии аспирин</context></output>
  </example>
  <example>
    <input>позвони в пятёрочку на ленина и узнай часы работы</input>
    <output><phone_number></phone_number><organization>пятёрочка на ленина</organization><context>узнай часы работы</context></output>
  </example>
</examples>`
)

var digitsOnly = regexp.MustCompile(`\D`)

// llmParsedFlexible — YandexGPT иногда оборачивает ответ в <output>, иногда кладёт теги в корень;
// иногда добавляет markdown ``` вокруг XML.
type llmParsedFlexible struct {
	PhoneDirect string `xml:"phone_number"`
	OrgDirect   string `xml:"organization"`
	CtxDirect   string `xml:"context"`
	Output      struct {
		Phone string `xml:"phone_number"`
		Org   string `xml:"organization"`
		Ctx   string `xml:"context"`
	} `xml:"output"`
}

func (f llmParsedFlexible) fields() (phone, org, ctx string) {
	p, o, c := strings.TrimSpace(f.Output.Phone), strings.TrimSpace(f.Output.Org), strings.TrimSpace(f.Output.Ctx)
	if p != "" || o != "" || c != "" {
		return p, o, c
	}
	return strings.TrimSpace(f.PhoneDirect), strings.TrimSpace(f.OrgDirect), strings.TrimSpace(f.CtxDirect)
}

var rePhoneTag = regexp.MustCompile(`(?i)<phone_number>\s*([^<]*?)\s*</phone_number>`)
var reOrgTag = regexp.MustCompile(`(?i)<organization>\s*([^<]*?)\s*</organization>`)
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

func extractTagsFallback(s string) (phone, org, ctx string) {
	if m := rePhoneTag.FindStringSubmatch(s); len(m) > 1 {
		phone = strings.TrimSpace(m[1])
	}
	if m := reOrgTag.FindStringSubmatch(s); len(m) > 1 {
		org = strings.TrimSpace(m[1])
	}
	if m := reCtxTag.FindStringSubmatch(s); len(m) > 1 {
		ctx = strings.TrimSpace(m[1])
	}
	return phone, org, ctx
}

type Result struct {
	PhoneNumber  string // 11 цифр без пробелов, например 79991234567
	Context      string // цель звонка без номера
	Organization string // заполняется, если номер найден по названию организации
	DisplayName  string // описание найденной точки (название + адрес), если резолвили
	IsHotline    bool   // номер похож на федеральную горячую линию (8-800)
}

// PhoneResolver находит телефон организации по её свободному названию.
type PhoneResolver interface {
	Resolve(ctx context.Context, query string) (*orgsearch.Result, error)
}

type Parser struct {
	apiKey   string
	folderID string
	resolver PhoneResolver
	client   *http.Client
}

func New(apiKey, folderID string, resolver PhoneResolver) *Parser {
	return &Parser{
		apiKey:   apiKey,
		folderID: folderID,
		resolver: resolver,
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
	rawPhone, rawOrg, rawCtx := "", "", ""
	if err := xml.Unmarshal([]byte("<root>"+rawText+"</root>"), &flex); err == nil {
		rawPhone, rawOrg, rawCtx = flex.fields()
	}
	if rawPhone == "" && rawOrg == "" {
		rawPhone, rawOrg, rawCtx = extractTagsFallback(rawText)
	}

	// Явно указанный номер имеет приоритет.
	digits := digitsOnly.ReplaceAllString(rawPhone, "")
	if len(digits) == 11 {
		return &Result{PhoneNumber: digits, Context: rawCtx}, nil
	}

	// Номера нет — пробуем найти его по названию организации.
	if rawOrg != "" && p.resolver != nil {
		res, err := p.resolver.Resolve(ctx, rawOrg)
		if err != nil {
			return nil, err
		}
		if res == nil || len(res.Phone) != 11 {
			return nil, fmt.Errorf("резолвер вернул некорректный номер")
		}
		return &Result{
			PhoneNumber:  res.Phone,
			Context:      rawCtx,
			Organization: rawOrg,
			DisplayName:  res.DisplayName,
			IsHotline:    res.IsHotline,
		}, nil
	}

	if rawPhone != "" {
		return nil, fmt.Errorf("извлечённый номер имеет неверный формат: %q", rawPhone)
	}
	return nil, fmt.Errorf("не удалось определить номер телефона или организацию из сообщения")
}
