// Package orgsearch резолвит телефон организации по свободному названию
// через Yandex Cloud Search API v2 (генеративный поиск).
package orgsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const genSearchURL = "https://searchapi.api.cloud.yandex.net/v2/gen/search"

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
		client:   &http.Client{Timeout: 20 * time.Second},
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

var rePhone = regexp.MustCompile(`(?:\+7|\b8|\b7)[\s\-(]*\d{3}[\s\-)]*\d{3}[\s\-]*\d{2}[\s\-]*\d{2}`)
var nonDigit = regexp.MustCompile(`\D`)

// Resolve возвращает нормализованный 11-значный телефон и краткое описание
// (первое предложение генеративного ответа) для названия организации query.
func (r *Resolver) Resolve(ctx context.Context, query string) (phone, displayName string, err error) {
	answer, err := r.search(ctx, query+" телефон")
	if err != nil {
		return "", "", err
	}
	phone = extractPhone(answer)
	if phone == "" {
		return "", "", fmt.Errorf("телефон организации %q не найден", query)
	}
	return phone, firstSentence(answer), nil
}

func (r *Resolver) search(ctx context.Context, query string) (string, error) {
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
		return "", fmt.Errorf("marshal search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, genSearchURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search API unexpected status: %d", resp.StatusCode)
	}

	var chunks []genChunk
	if err := json.NewDecoder(resp.Body).Decode(&chunks); err != nil {
		return "", fmt.Errorf("decode search response: %w", err)
	}

	// Чанки кумулятивные — берём самый длинный content (финальный ответ).
	best := ""
	for _, c := range chunks {
		if len(c.Message.Content) > len(best) {
			best = c.Message.Content
		}
	}
	if best == "" {
		return "", fmt.Errorf("search API вернул пустой ответ")
	}
	return best, nil
}

// extractPhone достаёт первый телефон РФ из текста и нормализует в 11 цифр.
func extractPhone(text string) string {
	m := rePhone.FindString(text)
	if m == "" {
		return ""
	}
	digits := nonDigit.ReplaceAllString(m, "")
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

// firstSentence возвращает первое предложение ответа (для показа пользователю).
func firstSentence(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
