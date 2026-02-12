package translator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FormatterChunk struct {
	FileName string
	MimeType string
	Data     []byte
}

type TextFormatter interface {
	Format(ctx context.Context, chunk FormatterChunk, chunkIndex int) (string, error)
}

func NewFormatter(cfg ProviderConfig) (TextFormatter, error) {
	cfg.Type = NormalizeProviderType(string(cfg.Type))
	cfg.MaxTokens = SanitizeMaxTokens(cfg.MaxTokens)
	switch cfg.Type {
	case ProviderTypeGemini:
		return newGeminiFormatter(cfg)
	case ProviderTypeAnthropic:
		return newAnthropicFormatter(cfg)
	default:
		return newOpenAIFormatter(cfg)
	}
}

const formatterSystemPrompt = "你是一名专业的中文文字编辑，擅长将长篇文本排版得整洁易读。请保持原文语义并优化段落、标题与列表的结构，不得遗漏或删除任何内容，也不要加入原文没有的信息。"

const formatterGuideline = `请遵守以下排版要求：
1. 保留章节标题与层级结构，但不要重复数字或额外加粗。
2. 删除页眉、页脚、页码（如“第323页”）以及重复的书名、作者信息。
3. 保持正文段落顺序与内容，不得删减或概括。
4. 使用空行分隔段落，列表请使用清晰的符号或编号。
5. 如遇表格或特殊排版，可用简明文字描述其结构。`

func buildFormatterInstruction(fileName string) string {
	return fmt.Sprintf("%s\n\n附件：%s\n请输出整理后的正文。", formatterGuideline, fileName)
}

type openAIFormatter struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
	maxTokens  int
}

func newOpenAIFormatter(cfg ProviderConfig) (TextFormatter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("OPENAI_MODEL 未配置")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBase
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	return &openAIFormatter{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		model:      cfg.Model,
		timeout:    cfg.Timeout,
		maxTokens:  cfg.MaxTokens,
	}, nil
}

func (f *openAIFormatter) Format(ctx context.Context, chunk FormatterChunk, chunkIndex int) (string, error) {
	textContent := string(chunk.Data)
	userPrompt := buildFormatterInstruction(chunk.FileName) + "\n\n文本内容：\n" + textContent
	payload := openAIChatRequest{
		Model:       f.model,
		MaxTokens:   f.maxTokens,
		Temperature: 0.1,
		TopP:        0.95,
		Messages: []openAIMessage{
			{
				Role: "system",
				Content: []openAIMessagePart{
					{Type: "text", Text: formatterSystemPrompt},
				},
			},
			{
				Role: "user",
				Content: []openAIMessagePart{
					{Type: "text", Text: userPrompt},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.apiKey)

	logFormatterRequest("OpenAI", chunkIndex, payload)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 OpenAI Formatter 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimitedBytes(resp.Body, 1<<20)
		logFormatterHTTPError("OpenAI", chunkIndex, resp.StatusCode, data)
		return "", fmt.Errorf("OpenAI Formatter 响应错误: %s", resp.Status)
	}

	var parsed openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("解析 OpenAI Formatter 响应失败: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("OpenAI Formatter 返回为空")
	}
	logFormatterResponse("OpenAI", chunkIndex, parsed.Choices[0].Message.Content)
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func (f *openAIFormatter) chatEndpoint() string {
	if strings.HasSuffix(f.baseURL, "/chat/completions") {
		return f.baseURL
	}
	return strings.TrimRight(f.baseURL, "/") + "/chat/completions"
}

type geminiFormatter struct {
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client
	maxTokens  int
}

func newGeminiFormatter(cfg ProviderConfig) (TextFormatter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("Gemini API Key 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("Gemini 模型未配置")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultGeminiBase
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	return &geminiFormatter{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		model:      cfg.Model,
		timeout:    cfg.Timeout,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		maxTokens:  cfg.MaxTokens,
	}, nil
}

func (f *geminiFormatter) Format(ctx context.Context, chunk FormatterChunk, chunkIndex int) (string, error) {
	reqBody := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: formatterSystemPrompt}},
		},
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: buildFormatterInstruction(chunk.FileName)},
					{
						InlineData: &geminiInlineData{
							MIME: chunk.MimeType,
							Data: base64.StdEncoding.EncodeToString(chunk.Data),
						},
					},
				},
			},
		},
		GenerationConfig: geminiGeneration{
			MaxOutputToken: f.maxTokens,
			Temperature:    0.2,
		},
	}
	body, _ := json.Marshal(reqBody)
	endpoint := f.buildEndpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", f.apiKey)

	logFormatterRequest("Gemini", chunkIndex, reqBody)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 Gemini Formatter 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimited(resp.Body, 1<<20)
		logFormatterHTTPError("Gemini", chunkIndex, resp.StatusCode, data)
		return "", fmt.Errorf("Gemini Formatter 响应错误: %s", resp.Status)
	}

	var parsed geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("解析 Gemini Formatter 响应失败: %w", err)
	}
	text := strings.TrimSpace(parsed.FirstText())
	if text == "" {
		return "", fmt.Errorf("Gemini Formatter 返回空内容")
	}
	logFormatterResponse("Gemini", chunkIndex, text)
	return text, nil
}

func (f *geminiFormatter) buildEndpoint() string {
	base := strings.TrimRight(f.baseURL, "/")
	if strings.Contains(base, "/models/") {
		if strings.Contains(base, ":generateContent") {
			return base
		}
		return base + ":generateContent"
	}
	if !strings.Contains(base, "/v1beta") {
		base = base + "/v1beta"
	}
	return fmt.Sprintf("%s/models/%s:generateContent", base, url.PathEscape(f.model))
}

type anthropicFormatter struct {
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client
	maxTokens  int
}

func newAnthropicFormatter(cfg ProviderConfig) (TextFormatter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("Anthropic API Key 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("Anthropic 模型未配置")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultAnthropicBase
	}
	if !strings.HasSuffix(baseURL, "/v1/messages") {
		baseURL = baseURL + "/v1/messages"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	return &anthropicFormatter{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		model:      cfg.Model,
		timeout:    cfg.Timeout,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		maxTokens:  cfg.MaxTokens,
	}, nil
}

func (f *anthropicFormatter) Format(ctx context.Context, chunk FormatterChunk, chunkIndex int) (string, error) {
	reqBody := anthropicRequest{
		Model:       f.model,
		System:      formatterSystemPrompt,
		MaxTokens:   f.maxTokens,
		Temperature: 0.2,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContent{
					{Type: "text", Text: buildFormatterInstruction(chunk.FileName)},
					{
						Type: "image",
						Source: &anthropicImageSource{
							Type:      "base64",
							MediaType: chunk.MimeType,
							Data:      base64.StdEncoding.EncodeToString(chunk.Data),
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", f.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	logFormatterRequest("Anthropic", chunkIndex, reqBody)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 Anthropic Formatter 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimited(resp.Body, 1<<20)
		logFormatterHTTPError("Anthropic", chunkIndex, resp.StatusCode, data)
		return "", fmt.Errorf("Anthropic Formatter 响应错误: %s", resp.Status)
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("解析 Anthropic Formatter 响应失败: %w", err)
	}
	text := strings.TrimSpace(parsed.FirstText())
	if text == "" {
		return "", fmt.Errorf("Anthropic Formatter 返回空内容")
	}
	logFormatterResponse("Anthropic", chunkIndex, text)
	return text, nil
}

func logFormatterRequest(provider string, chunk int, payload interface{}) {
	var body []byte
	switch p := payload.(type) {
	case geminiRequest:
		body, _ = json.MarshalIndent(maskGeminiFormatterPayload(p), "", "  ")
	case anthropicRequest:
		body, _ = json.MarshalIndent(maskAnthropicFormatterPayload(p), "", "  ")
	default:
		body, _ = json.MarshalIndent(payload, "", "  ")
	}
	log.Printf("[%s Formatter] Chunk %d 请求:\n%s", provider, chunk, string(body))
}

func logFormatterResponse(provider string, chunk int, content string) {
	log.Printf("[%s Formatter] Chunk %d 响应:\n%s", provider, chunk, content)
}

func logFormatterHTTPError(provider string, chunk int, status int, body []byte) {
	log.Printf("[%s Formatter] Chunk %d HTTP %d: %s", provider, chunk, status, string(body))
}

func maskGeminiFormatterPayload(req geminiRequest) geminiRequest {
	clone := req
	for i := range clone.Contents {
		for j := range clone.Contents[i].Parts {
			if clone.Contents[i].Parts[j].InlineData != nil {
				clone.Contents[i].Parts[j].InlineData.Data = fmt.Sprintf("<inline base64 length=%d>", len(clone.Contents[i].Parts[j].InlineData.Data))
			}
		}
	}
	if clone.SystemInstruction != nil {
		for j := range clone.SystemInstruction.Parts {
			if clone.SystemInstruction.Parts[j].InlineData != nil {
				clone.SystemInstruction.Parts[j].InlineData.Data = fmt.Sprintf("<inline base64 length=%d>", len(clone.SystemInstruction.Parts[j].InlineData.Data))
			}
		}
	}
	return clone
}

func maskAnthropicFormatterPayload(req anthropicRequest) anthropicRequest {
	clone := req
	for i := range clone.Messages {
		for j := range clone.Messages[i].Content {
			if clone.Messages[i].Content[j].Source != nil {
				clone.Messages[i].Content[j].Source.Data = fmt.Sprintf("<inline base64 length=%d>", len(clone.Messages[i].Content[j].Source.Data))
			}
		}
	}
	return clone
}
