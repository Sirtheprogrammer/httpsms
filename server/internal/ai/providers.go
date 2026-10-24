// Package ai calls the chat models of the providers users bring their own keys for.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderDeepSeek  = "deepseek"
)

// ProviderInfo is shown in the dashboard when adding a key.
type ProviderInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
	KeyURL       string `json:"key_url"`
	KeyPrefix    string `json:"key_prefix"`
	BaseURLHint  string `json:"base_url_hint"`
}

// Providers lists supported providers. Model lists come live from each provider's
// /models endpoint (see ListModels), so only Anthropic gets a pinned default here.
var Providers = []ProviderInfo{
	{ID: ProviderOpenAI, Name: "OpenAI", KeyURL: "https://platform.openai.com/api-keys", KeyPrefix: "sk-", BaseURLHint: "https://api.openai.com/v1 (or any OpenAI-compatible endpoint)"},
	{ID: ProviderAnthropic, Name: "Anthropic", DefaultModel: "claude-opus-5-5", KeyURL: "https://console.anthropic.com/settings/keys", KeyPrefix: "sk-ant-"},
	{ID: ProviderGemini, Name: "Google Gemini", KeyURL: "https://aistudio.google.com/apikey", KeyPrefix: "AI"},
	{ID: ProviderDeepSeek, Name: "DeepSeek", KeyURL: "https://platform.deepseek.com/api_keys", KeyPrefix: "sk-"},
}

func IsProvider(id string) bool {
	for _, p := range Providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Message is one turn of a conversation. Role is "user" or "assistant".
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Provider  string
	APIKey    string
	BaseURL   string
	Model     string
	System    string
	Messages  []Message
	MaxTokens int
}

type Result struct {
	Text         string `json:"text"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

var httpClient = &http.Client{Timeout: 90 * time.Second}

// Complete runs one chat completion against the chosen provider.
func Complete(ctx context.Context, req Request) (*Result, error) {
	if req.Model == "" {
		return nil, errors.New("no model selected")
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 1024
	}
	req.Messages = normalizeTurns(req.Messages)
	if len(req.Messages) == 0 {
		return nil, errors.New("nothing to send to the model")
	}

	switch req.Provider {
	case ProviderAnthropic:
		return completeAnthropic(ctx, req)
	case ProviderOpenAI:
		return completeOpenAICompatible(ctx, req, firstNonEmpty(req.BaseURL, "https://api.openai.com/v1"), "max_completion_tokens")
	case ProviderDeepSeek:
		return completeOpenAICompatible(ctx, req, firstNonEmpty(req.BaseURL, "https://api.deepseek.com"), "max_tokens")
	case ProviderGemini:
		return completeGemini(ctx, req)
	default:
		return nil, fmt.Errorf("unknown provider %q", req.Provider)
	}
}

// normalizeTurns drops empty turns, merges consecutive same-role turns and makes
// the conversation start with the user, which every provider requires.
func normalizeTurns(in []Message) []Message {
	out := make([]Message, 0, len(in))
	for _, m := range in {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content += "\n\n" + text
			continue
		}
		out = append(out, Message{Role: role, Content: text})
	}
	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out
}

// --- Anthropic (official Go SDK) ---

// Models that accept the server-side refusal fallback in its "default" form on the
// Claude API: a declined request is re-served by a suitable model inside the same call.
var anthropicDefaultFallback = map[string]bool{
	"claude-fable-5-1":  true,
	"claude-opus-5-5":   true,
	"claude-opus-5":     true,
	"claude-sonnet-5-5": true,
}

func anthropicClient(apiKey, baseURL string) anthropic.Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(2)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return anthropic.NewClient(opts...)
}

func completeAnthropic(ctx context.Context, req Request) (*Result, error) {
	client := anthropicClient(req.APIKey, req.BaseURL)

	msgs := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)},
			})
		} else {
			msgs = append(msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Content)))
		}
	}

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if anthropicDefaultFallback[req.Model] && req.BaseURL == "" {
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}

	resp, err := client.Beta.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return nil, fmt.Errorf("Anthropic rejected the API key (HTTP %d)", apiErr.StatusCode)
			case http.StatusNotFound:
				return nil, fmt.Errorf("Anthropic model %q not found", req.Model)
			case http.StatusTooManyRequests:
				return nil, errors.New("Anthropic rate limit reached, try again shortly")
			}
			return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s", apiErr.StatusCode, apiErr.Error())
		}
		return nil, fmt.Errorf("Anthropic request failed: %w", err)
	}

	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return nil, errors.New("the model declined to answer this message")
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return &Result{
		Text:         strings.TrimSpace(sb.String()),
		Model:        string(resp.Model),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}, nil
}

// --- OpenAI and OpenAI-compatible (DeepSeek) ---

func completeOpenAICompatible(ctx context.Context, req Request, baseURL, maxTokensField string) (*Result, error) {
	messages := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}
	body := map[string]interface{}{
		"model":        req.Model,
		"messages":     messages,
		maxTokensField: req.MaxTokens,
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}
	if err := postJSON(ctx, strings.TrimRight(baseURL, "/")+"/chat/completions", headers, body, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("provider returned no choices")
	}
	if out.Choices[0].FinishReason == "content_filter" {
		return nil, errors.New("the provider's content filter blocked the reply")
	}
	return &Result{
		Text:         strings.TrimSpace(out.Choices[0].Message.Content),
		Model:        out.Model,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}

// --- Google Gemini ---

func geminiBase(baseURL string) string {
	return strings.TrimRight(firstNonEmpty(baseURL, "https://generativelanguage.googleapis.com/v1beta"), "/")
}

func completeGemini(ctx context.Context, req Request) (*Result, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	contents := make([]content, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, content{Role: role, Parts: []part{{Text: m.Content}}})
	}
	body := map[string]interface{}{
		"contents":         contents,
		"generationConfig": map[string]interface{}{"maxOutputTokens": req.MaxTokens},
	}
	if req.System != "" {
		body["systemInstruction"] = content{Parts: []part{{Text: req.System}}}
	}

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []part `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	model := strings.TrimPrefix(req.Model, "models/")
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", geminiBase(req.BaseURL), url.PathEscape(model))
	if err := postJSON(ctx, endpoint, map[string]string{"x-goog-api-key": req.APIKey}, body, &out); err != nil {
		return nil, err
	}
	if out.PromptFeedback.BlockReason != "" {
		return nil, fmt.Errorf("Gemini blocked the prompt (%s)", out.PromptFeedback.BlockReason)
	}
	if len(out.Candidates) == 0 {
		return nil, errors.New("Gemini returned no candidates")
	}
	var sb strings.Builder
	for _, p := range out.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	if sb.Len() == 0 && out.Candidates[0].FinishReason != "" && out.Candidates[0].FinishReason != "STOP" {
		return nil, fmt.Errorf("Gemini stopped without text (%s)", out.Candidates[0].FinishReason)
	}
	return &Result{
		Text:         strings.TrimSpace(sb.String()),
		Model:        firstNonEmpty(out.ModelVersion, model),
		InputTokens:  out.UsageMetadata.PromptTokenCount,
		OutputTokens: out.UsageMetadata.CandidatesTokenCount,
	}, nil
}

// ListModels validates a key and returns chat-capable model IDs for the picker.
func ListModels(ctx context.Context, provider, apiKey, baseURL string) ([]string, error) {
	var ids []string
	switch provider {
	case ProviderAnthropic:
		client := anthropicClient(apiKey, baseURL)
		pager := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
		for pager.Next() {
			ids = append(ids, pager.Current().ID)
		}
		if err := pager.Err(); err != nil {
			var apiErr *anthropic.Error
			if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
				return nil, errors.New("Anthropic rejected the API key")
			}
			return nil, fmt.Errorf("could not list Anthropic models: %w", err)
		}
		return ids, nil // newest first, as returned by the API

	case ProviderOpenAI, ProviderDeepSeek:
		base := firstNonEmpty(baseURL, "https://api.openai.com/v1")
		if provider == ProviderDeepSeek {
			base = firstNonEmpty(baseURL, "https://api.deepseek.com")
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getJSON(ctx, strings.TrimRight(base, "/")+"/models", map[string]string{"Authorization": "Bearer " + apiKey}, &out); err != nil {
			return nil, err
		}
		for _, m := range out.Data {
			if provider == ProviderOpenAI && baseURL == "" && !isOpenAIChatModel(m.ID) {
				continue
			}
			ids = append(ids, m.ID)
		}

	case ProviderGemini:
		var out struct {
			Models []struct {
				Name                       string   `json:"name"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err := getJSON(ctx, geminiBase(baseURL)+"/models?pageSize=200", map[string]string{"x-goog-api-key": apiKey}, &out); err != nil {
			return nil, err
		}
		for _, m := range out.Models {
			for _, method := range m.SupportedGenerationMethods {
				if method == "generateContent" {
					ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
					break
				}
			}
		}

	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	sort.Strings(ids)
	return ids, nil
}

// isOpenAIChatModel filters embeddings, audio, image and moderation models out of /v1/models.
func isOpenAIChatModel(id string) bool {
	for _, skip := range []string{"embedding", "whisper", "tts", "dall-e", "image", "moderation", "audio", "realtime", "transcribe", "search", "davinci", "babbage"} {
		if strings.Contains(id, skip) {
			return false
		}
	}
	return strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "o") || strings.HasPrefix(id, "chatgpt")
}

// --- HTTP helpers ---

func postJSON(ctx context.Context, endpoint string, headers map[string]string, body, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req, headers, out)
}

func getJSON(ctx context.Context, endpoint string, headers map[string]string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return do(req, headers, out)
}

func do(req *http.Request, headers map[string]string, out interface{}) error {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return providerError(req.URL.Host, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected response from %s: %w", req.URL.Host, err)
	}
	return nil
}

func providerError(host string, status int, raw []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	msg := e.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s rejected the API key: %s", host, msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s rate limit or quota reached: %s", host, msg)
	}
	return fmt.Errorf("%s returned HTTP %d: %s", host, status, msg)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
