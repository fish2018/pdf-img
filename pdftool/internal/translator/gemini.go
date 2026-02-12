package translator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type geminiTranslator struct {
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

const defaultGeminiBase = "https://generativelanguage.googleapis.com/v1beta"

func newGeminiTranslator(cfg ProviderConfig) (Translator, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("Gemini API Key 未配置")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("Gemini 模型未配置")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultGeminiBase
	}

	return &geminiTranslator{
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

func (t *geminiTranslator) Translate(ctx context.Context, imagePath string) (Result, error) {
	pageNumber := pageNumberFromContext(ctx)
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{}, fmt.Errorf("读取图片失败: %w", err)
	}
	mimeType := detectImageMIME(data)

	inline := geminiInlineData{
		MIME: mimeType,
		Data: base64.StdEncoding.EncodeToString(data),
	}
	userPrompt := t.userPrompt
	if t.optimizeLayout {
		userPrompt = userPrompt + " 请确保 sourceText 与 translatedText 字段在排版上保持清晰的段落、标题和列表结构。"
	}

	reqBody := geminiRequest{
		GenerationConfig: geminiGeneration{
			Temperature:    0.1,
			MaxOutputToken: t.maxTokens,
		},
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: userPrompt},
					{InlineData: &inline},
				},
			},
		},
	}
	if prompt := strings.TrimSpace(t.systemPrompt); prompt != "" {
		reqBody.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: prompt}},
		}
	}

	fullURL := t.buildEndpoint()
	bodyBytes, _ := json.Marshal(reqBody)
	logGeminiRequest(fullURL, reqBody, pageNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		logGeminiError(err, pageNumber)
		return Result{}, fmt.Errorf("调用 Gemini 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := readAllLimited(resp.Body, 1<<20)
		logGeminiHTTPError(resp.StatusCode, data, pageNumber)
		return Result{}, fmt.Errorf("Gemini 响应错误: %s", resp.Status)
	}

	var parsed geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("解析 Gemini 响应失败: %w", err)
	}
	logGeminiResponse(parsed, pageNumber)

	text := parsed.FirstText()
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("Gemini 返回空内容")
	}

	clean := cleanJSON(text)
	var payload struct {
		HasText        bool   `json:"hasText"`
		SourceText     string `json:"sourceText"`
		TranslatedText string `json:"translatedText"`
	}
	if err := json.Unmarshal([]byte(clean), &payload); err != nil {
		return Result{}, fmt.Errorf("解析 Gemini JSON 失败: %w", err)
	}
	return Result{
		HasText:        payload.HasText,
		SourceText:     payload.SourceText,
		TranslatedText: payload.TranslatedText,
	}, nil
}

func (t *geminiTranslator) buildEndpoint() string {
	base := t.baseURL
	if strings.Contains(base, "/models/") && strings.Contains(base, ":generateContent") {
		return base
	}
	base = strings.TrimRight(base, "/")
	if !strings.Contains(base, "/models/") {
		if !strings.HasSuffix(base, "/v1beta") {
			base = base + "/v1beta"
		}
		return fmt.Sprintf("%s/models/%s:generateContent", base, url.PathEscape(t.model))
	}
	// base includes /models/ but maybe missing action
	if !strings.Contains(base, ":") {
		return base + ":generateContent"
	}
	return base
}

type geminiRequest struct {
	SystemInstruction *geminiContent   `json:"system_instruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	GenerationConfig  geminiGeneration `json:"generationConfig"`
}

type geminiGeneration struct {
	Temperature    float64 `json:"temperature"`
	MaxOutputToken int     `json:"maxOutputTokens"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MIME string `json:"mime_type"`
	Data string `json:"data"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
}

func (r geminiResponse) FirstText() string {
	for _, cand := range r.Candidates {
		for _, part := range cand.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
}

func logGeminiRequest(endpoint string, payload geminiRequest, pageNumber int) {
	body, _ := json.MarshalIndent(maskGeminiPayload(payload), "", "  ")
	log.Printf("[Gemini] %s请求信息:\n  URL: %s\n  Headers: Content-Type=application/json, x-goog-api-key=***\n  Body:\n%s", formatPagePrefix(pageNumber), endpoint, string(body))
}

func logGeminiResponse(resp geminiResponse, pageNumber int) {
	data, _ := json.MarshalIndent(resp, "", "  ")
	log.Printf("[Gemini] %s响应信息:\n%s", formatPagePrefix(pageNumber), string(data))
}

func logGeminiError(err error, pageNumber int) {
	log.Printf("[Gemini] %s请求失败: %v", formatPagePrefix(pageNumber), err)
}

func logGeminiHTTPError(status int, body []byte, pageNumber int) {
	if pretty := formatJSONBody(body); pretty != "" {
		log.Printf("[Gemini] %sHTTP %d:\n%s", formatPagePrefix(pageNumber), status, pretty)
	} else {
		log.Printf("[Gemini] %sHTTP %d: %s", formatPagePrefix(pageNumber), status, string(body))
	}
}

func detectImageMIME(data []byte) string {
	if len(data) == 0 {
		return "image/png"
	}
	mime := http.DetectContentType(data)
	if strings.HasPrefix(mime, "image/") {
		return mime
	}
	return "image/png"
}

// readAllLimited prevents log bloat for large error bodies.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if limit <= 0 {
		limit = 1 << 20
	}
	limited := io.LimitReader(r, limit)
	_, err := buf.ReadFrom(limited)
	return buf.Bytes(), err
}

func formatJSONBody(body []byte) string {
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

func maskGeminiPayload(payload geminiRequest) geminiRequest {
	masked := payload
	for i := range masked.Contents {
		for j := range masked.Contents[i].Parts {
			if masked.Contents[i].Parts[j].InlineData != nil {
				masked.Contents[i].Parts[j].InlineData.Data = "<image base64 omitted>"
			}
		}
	}
	if masked.SystemInstruction != nil {
		for j := range masked.SystemInstruction.Parts {
			if masked.SystemInstruction.Parts[j].InlineData != nil {
				masked.SystemInstruction.Parts[j].InlineData.Data = "<image base64 omitted>"
			}
		}
	}
	return masked
}
