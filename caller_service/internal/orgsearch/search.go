// Package orgsearch резолвит телефон организации по свободному названию
// через Yandex Cloud Search API v2 (генеративный поиск).
package orgsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const genSearchURL = "https://searchapi.api.cloud.yandex.net/v2/gen/search"

const (
	maxAttempts  = 3
	retryBackoff = 1500 * time.Millisecond
)

// defaultSites — источники, в которых обычно есть телефоны организаций.
var defaultSites = []string{"yandex.ru", "2gis.ru", "zoon.ru", "orgpage.ru", "spr.ru"}

// SitesFromEnv читает список сайтов-источников из ORG_SEARCH_SITES
// (через запятую) либо возвращает defaultSites.
func SitesFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("ORG_SEARCH_SITES"))
	if raw == "" {
		return defaultSites
	}
	var sites []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			sites = append(sites, s)
		}
	}
	if len(sites) == 0 {
		return defaultSites
	}
	return sites
}

// Result — итог резолва организации.
type Result struct {
	Phone       string // 11 цифр, например 74956769922
	DisplayName string // краткое описание точки из ответа (название + адрес), без телефона
	IsHotline   bool   // номер похож на федеральную горячую линию (8-800)
}

// Resolver обращается к Search API v2 тем же Api-Key, что и остальной сервис.
type Resolver struct {
	apiKey   string
	folderID string
	sites    []string
	client   *http.Client
}

func New(apiKey, folderID string, sites []string) *Resolver {
	if len(sites) == 0 {
		sites = defaultSites
	}
	return &Resolver{
		apiKey:   apiKey,
		folderID: folderID,
		sites:    sites,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

type genRequest struct {
	Messages    []genMessage `json:"messages"`
	FolderID    string       `json:"folderId"`
	FixMisspell bool         `json:"fixMisspell"`
	Site        *siteFilter  `json:"site,omitempty"`
}

type siteFilter struct {
	Site []string `json:"site"`
}

type genMessage struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

// genChunk — элемент массива-ответа Search API v2 (стрим кумулятивных чанков).
type genChunk struct {
	Message struct {
		Content string `json:"content"`
		Role    string `json:"role"`
	} `json:"message"`
}

var (
	rePhone  = regexp.MustCompile(`(?:\+7|\b8|\b7)[\s\-(]*\d{3}[\s\-)]*\d{3}[\s\-]*\d{2}[\s\-]*\d{2}`)
	nonDigit = regexp.MustCompile(`\D`)
	reCite   = regexp.MustCompile(`\[\d+\]`)
)

// Resolve возвращает нормализованный 11-значный телефон и краткое описание точки
// для названия организации query.
func (r *Resolver) Resolve(ctx context.Context, query string) (*Result, error) {
	answer, err := r.search(ctx, query+" телефон")
	if err != nil {
		return nil, err
	}
	phoneMatch := rePhone.FindString(answer)
	phone := normalize(phoneMatch)
	if phone == "" {
		return nil, fmt.Errorf("телефон организации %q не найден", query)
	}
	return &Result{
		Phone:       phone,
		DisplayName: describe(answer, phoneMatch),
		IsHotline:   isHotline(phone),
	}, nil
}

// search делает запрос к Search API с ретраями и возвращает текст лучшего чанка.
func (r *Resolver) search(ctx context.Context, query string) (string, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(retryBackoff):
			}
		}

		content, status, err := r.doSearch(ctx, query)
		switch {
		case err != nil:
			lastErr = err
		case status == http.StatusTooManyRequests || status >= 500:
			lastErr = fmt.Errorf("search API status %d (rate limit / server)", status)
		case status != http.StatusOK:
			return "", fmt.Errorf("search API unexpected status: %d", status)
		case content == "":
			lastErr = fmt.Errorf("search API вернул пустой ответ")
		default:
			return content, nil
		}
	}
	return "", fmt.Errorf("search не удался после %d попыток: %w", maxAttempts, lastErr)
}

func (r *Resolver) doSearch(ctx context.Context, query string) (content string, status int, err error) {
	reqBody := genRequest{
		Messages:    []genMessage{{Content: query, Role: "ROLE_USER"}},
		FolderID:    r.folderID,
		FixMisspell: true,
	}
	if len(r.sites) > 0 {
		reqBody.Site = &siteFilter{Site: r.sites}
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("marshal search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, genSearchURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("read search response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}
	return bestContent(raw), resp.StatusCode, nil
}

// bestContent устойчиво разбирает ответ: массив чанков (часть элементов может быть
// строкой, а не объектом — такие пропускаем) либо одиночный объект; берёт самый
// длинный message.content
func bestContent(raw []byte) string {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		best := ""
		for _, el := range arr {
			var c genChunk
			if json.Unmarshal(el, &c) != nil {
				continue // не объект (например, строка) — пропускаем
			}
			if len(c.Message.Content) > len(best) {
				best = c.Message.Content
			}
		}
		return best
	}
	var one genChunk
	if json.Unmarshal(raw, &one) == nil {
		return one.Message.Content
	}
	return ""
}

// normalize приводит найденный телефон к 11 цифрам (8->7, 10-значный -> +7).
func normalize(match string) string {
	if match == "" {
		return ""
	}
	digits := nonDigit.ReplaceAllString(match, "")
	switch {
	case len(digits) == 11 && digits[0] == '8':
		digits = "7" + digits[1:]
	case len(digits) == 10:
		digits = "7" + digits
	}
	if len(digits) != 11 {
		return ""
	}
	return digits
}

func isHotline(phone string) bool {
	return len(phone) == 11 && strings.HasPrefix(phone, "7800")
}

func describe(answer, phoneMatch string) string {
	s := answer
	if phoneMatch != "" {
		s = strings.Replace(s, phoneMatch, "", 1)
	}
	s = strings.ReplaceAll(s, "*", "")
	s = reCite.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ") // схлопывает пробелы, включая \xa0

	s = strings.TrimLeft(s, "—–-:•·,. ")
	low := strings.ToLower(s)
	for _, p := range []string{"номер телефона", "телефон"} {
		if strings.HasPrefix(low, p) {
			s = strings.TrimSpace(s[len(p):])
			s = strings.TrimLeft(s, "—–-:•·,. ")
			break
		}
	}

	const maxLen = 200
	if r := []rune(s); len(r) > maxLen {
		s = strings.TrimSpace(string(r[:maxLen])) + "…"
	}
	return s
}
