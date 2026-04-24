package semantic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sjzar/chatlog/internal/chatlog/conf"
)

const (
	maxEmbeddingBatch       = 64
	maxEmbeddingInputTokens = 3072
	maxRerankTotalChars     = 30000 // GLM rerank limits query+documents to 32k chars
	maxRerankDocs           = 80    // cap docs sent to reranker
	maxRerankDocChars       = 400   // per-doc char ceiling for reranker
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 45 * time.Second},
	}
}

func (c *Client) Test(ctx context.Context, cfg conf.SemanticConfig) error {
	if _, err := c.Embed(ctx, cfg, []string{"连通性测试"}); err != nil {
		return err
	}
	_, err := c.Chat(ctx, cfg, []ChatMessage{
		{Role: "user", Content: "请用一句话回复：连接正常。"},
	})
	return err
}

func (c *Client) Embed(ctx context.Context, cfg conf.SemanticConfig, inputs []string) ([][]float64, error) {
	inputs = sanitizeInputs(inputs)
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float64, 0, len(inputs))
	for i := 0; i < len(inputs); i += maxEmbeddingBatch {
		end := i + maxEmbeddingBatch
		if end > len(inputs) {
			end = len(inputs)
		}
		vecs, err := c.embedBatch(ctx, cfg, inputs[i:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, cfg conf.SemanticConfig, inputs []string) ([][]float64, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = conf.DefaultGLMBaseURL
	}
	payload := map[string]any{
		"model": cfg.EmbeddingModel,
		"input": inputs,
	}
	if cfg.EmbeddingDimension > 0 {
		payload["dimensions"] = cfg.EmbeddingDimension
	}
	var resp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Error map[string]any `json:"error"`
	}
	if err := c.doJSON(ctx, cfg.APIKey, base+"/embeddings", payload, &resp); err != nil {
		return nil, err
	}
	if len(resp.Error) > 0 {
		return nil, fmt.Errorf("glm embedding error: %v", resp.Error)
	}
	out := make([][]float64, len(inputs))
	for _, item := range resp.Data {
		if item.Index >= 0 && item.Index < len(out) {
			out[item.Index] = item.Embedding
		}
	}
	for i := range out {
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("glm embedding missing vector at index %d", i)
		}
	}
	return out, nil
}

type RerankItem struct {
	Index int
	Score float64
}

func (c *Client) Rerank(ctx context.Context, cfg conf.SemanticConfig, query string, docs []string, topN int) ([]RerankItem, error) {
	query = strings.TrimSpace(query)
	docs = sanitizeInputs(docs)
	if query == "" || len(docs) == 0 {
		return nil, nil
	}
	// Enforce GLM's 32k-char limit on query+documents by capping doc count,
	// truncating each doc, and ensuring the total fits the budget.
	if len(docs) > maxRerankDocs {
		docs = docs[:maxRerankDocs]
		if topN > maxRerankDocs {
			topN = maxRerankDocs
		}
	}
	queryChars := len([]rune(query))
	perDocBudget := (maxRerankTotalChars - queryChars) / len(docs)
	if perDocBudget < 80 {
		perDocBudget = 80
	}
	if perDocBudget > maxRerankDocChars {
		perDocBudget = maxRerankDocChars
	}
	for i := range docs {
		runes := []rune(docs[i])
		if len(runes) > perDocBudget {
			docs[i] = string(runes[:perDocBudget])
		}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = conf.DefaultGLMBaseURL
	}
	if topN <= 0 || topN > len(docs) {
		topN = len(docs)
	}
	payload := map[string]any{
		"model":            cfg.RerankModel,
		"query":            query,
		"documents":        docs,
		"top_n":            topN,
		"return_documents": false,
	}
	var resp struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
		Error map[string]any `json:"error"`
	}
	if err := c.doJSON(ctx, cfg.APIKey, base+"/rerank", payload, &resp); err != nil {
		return nil, err
	}
	if len(resp.Error) > 0 {
		return nil, fmt.Errorf("glm rerank error: %v", resp.Error)
	}
	out := make([]RerankItem, 0, len(resp.Results))
	for _, item := range resp.Results {
		out = append(out, RerankItem{
			Index: item.Index,
			Score: item.RelevanceScore,
		})
	}
	return out, nil
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *Client) Chat(ctx context.Context, cfg conf.SemanticConfig, messages []ChatMessage) (string, error) {
	clean := make([]ChatMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		clean = append(clean, ChatMessage{Role: role, Content: content})
	}
	if len(clean) == 0 {
		return "", nil
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = conf.DefaultGLMBaseURL
	}
	thinkingType := "disabled"
	if cfg.ChatThinking {
		thinkingType = "enabled"
	}
	payload := map[string]any{
		"model":       cfg.ChatModel,
		"messages":    clean,
		"thinking":    map[string]any{"type": thinkingType},
		"max_tokens":  cfg.ChatMaxTokens,
		"temperature": cfg.ChatTemperature,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error map[string]any `json:"error"`
	}
	if err := c.doJSON(ctx, cfg.APIKey, base+"/chat/completions", payload, &resp); err != nil {
		return "", err
	}
	if len(resp.Error) > 0 {
		return "", fmt.Errorf("glm chat error: %v", resp.Error)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("glm chat returned empty choices")
	}
	answer := strings.TrimSpace(resp.Choices[0].Message.Content)
	if answer == "" {
		return "", fmt.Errorf("glm chat returned empty content")
	}
	return answer, nil
}

func (c *Client) ChatStream(ctx context.Context, cfg conf.SemanticConfig, messages []ChatMessage, onDelta func(string) error) error {
	clean := make([]ChatMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		clean = append(clean, ChatMessage{Role: role, Content: content})
	}
	if len(clean) == 0 {
		return nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("glm api key is empty")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = conf.DefaultGLMBaseURL
	}
	thinkingType := "disabled"
	if cfg.ChatThinking {
		thinkingType = "enabled"
	}
	payload := map[string]any{
		"model":       cfg.ChatModel,
		"messages":    clean,
		"thinking":    map[string]any{"type": thinkingType},
		"stream":      true,
		"max_tokens":  cfg.ChatMaxTokens,
		"temperature": cfg.ChatTemperature,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		return fmt.Errorf("glm http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		delta, err := parseChatStreamDelta([]byte(data))
		if err != nil {
			return err
		}
		if delta == "" {
			continue
		}
		if onDelta != nil {
			if err := onDelta(delta); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func parseChatStreamDelta(raw []byte) (string, error) {
	var payload struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode glm stream failed: %w; response_snippet=%q", err, trimSnippet(raw, 260))
	}
	if len(payload.Error) > 0 {
		return "", fmt.Errorf("glm chat stream error: %v", payload.Error)
	}
	if len(payload.Choices) == 0 {
		return "", nil
	}
	if payload.Choices[0].Delta.Content != "" {
		return payload.Choices[0].Delta.Content, nil
	}
	return payload.Choices[0].Message.Content, nil
}

func (c *Client) doJSON(ctx context.Context, apiKey, url string, reqBody any, out any) error {
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("glm api key is empty")
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // utf-8 BOM guard
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("glm http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// Some upstream gateways occasionally return malformed numeric literals like "0. 123".
		// Repair this specific pattern and retry once.
		if strings.Contains(err.Error(), "after decimal point in numeric literal") {
			if fixed := fixBrokenJSONNumbers(raw); len(fixed) > 0 && !bytes.Equal(fixed, raw) {
				if err2 := json.Unmarshal(fixed, out); err2 == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("decode glm response failed: %w; response_snippet=%q", err, trimSnippet(raw, 260))
	}
	return nil
}

func sanitizeInputs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, truncateApproxTokens(item, maxEmbeddingInputTokens))
	}
	return out
}

func truncateApproxTokens(s string, maxTokens int) string {
	if maxTokens <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxTokens {
		return s
	}
	return string(runes[:maxTokens])
}

var brokenNumRe = regexp.MustCompile(`([0-9])\.\s+([0-9])`)

func fixBrokenJSONNumbers(in []byte) []byte {
	// Best-effort repair only; keeps behavior unchanged for valid JSON.
	s := string(in)
	for i := 0; i < 8; i++ {
		next := brokenNumRe.ReplaceAllString(s, `$1.$2`)
		if next == s {
			break
		}
		s = next
	}
	return []byte(s)
}

func trimSnippet(in []byte, n int) string {
	s := strings.TrimSpace(string(in))
	if n <= 0 || len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}
