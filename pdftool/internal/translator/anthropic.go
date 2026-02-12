package translator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultAnthropicBase = "https://api.anthropic.com/v1"

type anthropicTranslator struct {
	baseURL        string
	apiKey         string
	model          string
	timeout        time.Duration
	httpClient     *http.Client
	systemPrompt   string
	userPrompt     string
	maxTokens      int
	optimizeLayout bool
}

func newAnthropicTranslator(cfg ProviderConfig) (Translator, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("Anthropic API Key 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("Anthropic 模型未配置")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultAnthropicBase
	}
	if !strings.HasSuffix(baseURL, "/v1/messages") {
		baseURL = baseURL + "/v1/messages"
	}

	return &anthropicTranslator{
		baseURL:   baseURL,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		timeout:   cfg.Timeout,
		maxTokens: SanitizeMaxTokens(cfg.MaxTokens),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		systemPrompt:   "你是一个专业的OCR与翻译助手。阅读用户提供的图片，先识别出存在的文本，再将其翻译为简体中文。必须输出严格的JSON对象，格式为 {\"hasText\":bool,\"sourceText\":\"原始文本\",\"translatedText\":\"翻译后的文本\"} 。如果图片中没有文本，设置 hasText 为 false，另外两个字段留空字符串。",
		userPrompt:     "请识别这页图像中的所有可见文本并翻译成简体中文。保持原本的段落顺序，返回JSON字符串。",
		optimizeLayout: cfg.OptimizeLayout,
	}, nil
}

func (t *anthropicTranslator) Translate(ctx context.Context, imagePath string) (Result, error) {
	pageNumber := pageNumberFromContext(ctx)
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{}, fmt.Errorf("读取图片失败: %w", err)
	}
	mimeType := detectImageMIME(data)

	userPrompt := t.userPrompt
	if t.optimizeLayout {
		userPrompt = userPrompt + " 请在返回的 sourceText 与 translatedText 中保持良好的排版结构，保留标题、列表和空行。"
	}

	reqBody := anthropicRequest{
		Model:       t.model,
		MaxTokens:   t.maxTokens,
		System:      t.systemPrompt,
		Temperature: 0.1,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContent{
					{Type: "text", Text: userPrompt},
					{
						Type: "image",
						Source: &anthropicImageSource{
							Type:      "base64",
							MediaType: mimeType,
							Data:      base64.StdEncoding.EncodeToString(data),
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	logAnthropicRequest(t.baseURL, reqBody, pageNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		logAnthropicError(err, pageNumber)
		return Result{}, fmt.Errorf("调用 Anthropic 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimited(resp.Body, 1<<20)
		logAnthropicHTTPError(resp.StatusCode, data, pageNumber)
		return Result{}, fmt.Errorf("Anthropic 响应错误: %s", resp.Status)
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("解析 Anthropic 响应失败: %w", err)
	}
	logAnthropicResponse(parsed, pageNumber)

	text := parsed.FirstText()
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("Anthropic 返回空内容")
	}

	clean := cleanJSON(text)
	var payload struct {
		HasText        bool   `json:"hasText"`
		SourceText     string `json:"sourceText"`
		TranslatedText string `json:"translatedText"`
	}
	if err := json.Unmarshal([]byte(clean), &payload); err != nil {
		return Result{}, fmt.Errorf("解析 Anthropic JSON 失败: %w", err)
	}
	return Result{
		HasText:        payload.HasText,
		SourceText:     payload.SourceText,
		TranslatedText: payload.TranslatedText,
	}, nil
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

func (r anthropicResponse) FirstText() string {
	for _, item := range r.Content {
		if strings.TrimSpace(item.Text) != "" {
			return item.Text
		}
	}
	return ""
}

func logAnthropicRequest(endpoint string, payload anthropicRequest, pageNumber int) {
	body, _ := json.MarshalIndent(maskAnthropicPayload(payload), "", "  ")
	log.Printf("[Anthropic] %s请求信息:\n  URL: %s\n  Headers: Content-Type=application/json, x-api-key=***\n  Body:\n%s", formatPagePrefix(pageNumber), endpoint, string(body))
}

func logAnthropicResponse(resp anthropicResponse, pageNumber int) {
	data, _ := json.MarshalIndent(resp, "", "  ")
	log.Printf("[Anthropic] %s响应信息:\n%s", formatPagePrefix(pageNumber), string(data))
}

func logAnthropicError(err error, pageNumber int) {
	log.Printf("[Anthropic] %s请求失败: %v", formatPagePrefix(pageNumber), err)
}

func logAnthropicHTTPError(status int, body []byte, pageNumber int) {
	log.Printf("[Anthropic] %sHTTP %d: %s", formatPagePrefix(pageNumber), status, string(body))
}

func maskAnthropicPayload(payload anthropicRequest) anthropicRequest {
	masked := payload
	for mi := range masked.Messages {
		for ci := range masked.Messages[mi].Content {
			part := &masked.Messages[mi].Content[ci]
			if part.Source != nil {
				part.Source.Data = "<image base64 omitted>"
			}
		}
	}
	return masked
}
