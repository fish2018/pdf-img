package translator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Result captures the structured translation output.
type Result struct {
	HasText        bool
	SourceText     string
	TranslatedText string
}

// Translator describes the behavior needed by the service layer.
type Translator interface {
	Translate(ctx context.Context, imagePath string) (Result, error)
}

type openAITranslator struct {
	httpClient     *http.Client
	baseURL        string
	apiKey         string
	model          string
	timeout        time.Duration
	systemPrompt   string
	userPrompt     string
	maxTokens      int
	optimizeLayout bool
}

const defaultOpenAIBase = "https://api.openai.com/v1"

func newOpenAITranslator(cfg ProviderConfig) (Translator, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("OPENAI_MODEL 未配置")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 90 * time.Second
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBase
	}

	return &openAITranslator{
		httpClient:     &http.Client{Timeout: cfg.Timeout},
		baseURL:        baseURL,
		apiKey:         strings.TrimSpace(cfg.APIKey),
		model:          cfg.Model,
		timeout:        cfg.Timeout,
		maxTokens:      SanitizeMaxTokens(cfg.MaxTokens),
		systemPrompt:   "你是一个专业的OCR与翻译助手。阅读用户提供的图片，先识别出存在的文本，再将其翻译为简体中文。必须输出严格的JSON对象，格式为 {\"hasText\":bool,\"sourceText\":\"原始文本\",\"translatedText\":\"翻译后的文本\"} 。如果图片中没有文本，设置 hasText 为 false，另外两个字段留空字符串。",
		userPrompt:     "请识别这页图像中的所有可见文本并翻译成简体中文。保持原本的段落顺序，返回JSON字符串。",
		optimizeLayout: cfg.OptimizeLayout,
	}, nil
}

func (t *openAITranslator) Translate(ctx context.Context, imagePath string) (Result, error) {
	pageNumber := pageNumberFromContext(ctx)
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{}, fmt.Errorf("读取图片失败: %w", err)
	}

	content := fmt.Sprintf("data:%s;base64,%s", detectImageMIME(data), base64.StdEncoding.EncodeToString(data))
	userPrompt := t.userPrompt
	if t.optimizeLayout {
		userPrompt = userPrompt + " 请在 sourceText 与 translatedText 字段中保持原文的结构与排版，保留标题、列表和空行，使译文更整洁易读。"
	}

	payload := openAIChatRequest{
		Model:       t.model,
		MaxTokens:   t.maxTokens,
		Temperature: 0.1,
		TopP:        0.95,
		Messages: []openAIMessage{
			{
				Role:    "system",
				Content: t.systemPrompt,
			},
			{
				Role: "user",
				Content: []openAIMessagePart{
					{Type: "text", Text: userPrompt},
					{
						Type: "image_url",
						ImageURL: &openAIImageURL{
							URL: content,
						},
					},
				},
			},
		},
	}

	logOpenAIRequest(t.baseURL, payload, pageNumber)

	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		logOpenAIError(err, pageNumber)
		return Result{}, fmt.Errorf("调用OpenAI失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimitedBytes(resp.Body, 1<<20)
		logOpenAIHTTPError(resp.StatusCode, data, pageNumber)
		return Result{}, fmt.Errorf("OpenAI 响应错误: %s", resp.Status)
	}

	var parsed openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("解析OpenAI响应失败: %w", err)
	}

	if len(parsed.Choices) == 0 {
		return Result{}, fmt.Errorf("OpenAI 返回为空")
	}

	logOpenAIResponse(parsed, pageNumber)

	raw := strings.TrimSpace(parsed.Choices[0].Message.Content)
	clean := cleanJSON(raw)

	var resultPayload struct {
		HasText        bool   `json:"hasText"`
		SourceText     string `json:"sourceText"`
		TranslatedText string `json:"translatedText"`
	}
	if err := json.Unmarshal([]byte(clean), &resultPayload); err != nil {
		return Result{}, fmt.Errorf("解析OpenAI响应失败: %w", err)
	}
	return Result{
		HasText:        resultPayload.HasText,
		SourceText:     resultPayload.SourceText,
		TranslatedText: resultPayload.TranslatedText,
	}, nil
}

func (t *openAITranslator) chatEndpoint() string {
	if strings.HasSuffix(t.baseURL, "/chat/completions") {
		return t.baseURL
	}
	return strings.TrimRight(t.baseURL, "/") + "/chat/completions"
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
	TopP        float64         `json:"top_p,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type openAIMessagePart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func logOpenAIRequest(baseURL string, payload openAIChatRequest, pageNumber int) {
	body, _ := json.MarshalIndent(maskOpenAIPayload(payload), "", "  ")
	log.Printf("[OpenAI] %s请求信息:\n  URL: %s/chat/completions\n  Headers: Content-Type=application/json, Authorization=Bearer ***\n  Body:\n%s", formatPagePrefix(pageNumber), baseURL, string(body))
}

func logOpenAIResponse(resp openAIChatResponse, pageNumber int) {
	info := struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int    `json:"index"`
			Status  string `json:"status"`
			Content string `json:"content"`
		} `json:"choices"`
	}{
		ID:    resp.ID,
		Model: resp.Model,
	}
	for _, choice := range resp.Choices {
		info.Choices = append(info.Choices, struct {
			Index   int    `json:"index"`
			Status  string `json:"status"`
			Content string `json:"content"`
		}{
			Index:   choice.Index,
			Status:  choice.FinishReason,
			Content: strings.TrimSpace(choice.Message.Content),
		})
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	log.Printf("[OpenAI] %s响应信息:\n%s", formatPagePrefix(pageNumber), string(data))
}

func logOpenAIHTTPError(status int, body []byte, pageNumber int) {
	if pretty := formatJSON(body); pretty != "" {
		log.Printf("[OpenAI] %sHTTP %d:\n%s", formatPagePrefix(pageNumber), status, pretty)
		return
	}
	log.Printf("[OpenAI] %sHTTP %d: %s", formatPagePrefix(pageNumber), status, string(body))
}

func logOpenAIError(err error, pageNumber int) {
	if err == nil {
		return
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		log.Printf("[OpenAI] %s底层网络错误: %v", formatPagePrefix(pageNumber), urlErr)
	}
	log.Printf("[OpenAI] %s请求失败: %v", formatPagePrefix(pageNumber), err)
}

func maskOpenAIPayload(payload openAIChatRequest) openAIChatRequest {
	masked := payload
	masked.Messages = make([]openAIMessage, len(payload.Messages))
	for i, msg := range payload.Messages {
		masked.Messages[i].Role = msg.Role
		switch content := msg.Content.(type) {
		case string:
			masked.Messages[i].Content = content
		case []openAIMessagePart:
			parts := make([]openAIMessagePart, len(content))
			for j, part := range content {
				parts[j] = part
				if parts[j].ImageURL != nil {
					parts[j].ImageURL = &openAIImageURL{
						URL: maskDataURI(part.ImageURL.URL),
					}
				}
			}
			masked.Messages[i].Content = parts
		default:
			masked.Messages[i].Content = content
		}
	}
	return masked
}

func maskDataURI(raw string) string {
	if !strings.HasPrefix(raw, "data:") {
		return fmt.Sprintf("<image base64, length=%d>", len(raw))
	}
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return fmt.Sprintf("<image base64, length=%d>", len(raw))
	}
	header := parts[0]
	payload := parts[1]
	if len(payload) <= 12 {
		return fmt.Sprintf("%s,%s", header, "***")
	}
	head := payload[:12]
	tail := ""
	if len(payload) > 18 {
		tail = payload[len(payload)-6:]
	}
	return fmt.Sprintf("%s,%s***%s", header, head, tail)
}

func readAllLimitedBytes(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if limit <= 0 {
		limit = 1 << 20
	}
	limited := io.LimitReader(r, limit)
	_, err := buf.ReadFrom(limited)
	return buf.Bytes(), err
}

func formatJSON(body []byte) string {
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return ""
	}
	return string(pretty)
}

func cleanJSON(input string) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "```") {
		lines := strings.Split(input, "\n")
		var body []string
		for _, line := range lines {
			lineTrim := strings.TrimSpace(line)
			if strings.HasPrefix(lineTrim, "```") {
				continue
			}
			body = append(body, line)
		}
		input = strings.Join(body, "\n")
	}
	return strings.TrimSpace(input)
}
