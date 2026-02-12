package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []messagePart `json:"content"`
}

type messagePart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *messageImageURL `json:"image_url,omitempty"`
}

type messageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type logEntry struct {
	Timestamp string        `json:"timestamp"`
	Request   requestEntry  `json:"request"`
	Response  responseEntry `json:"response"`
}

type requestEntry struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type responseEntry struct {
	Status  string            `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func main() {
	var (
		imagePath = flag.String("image", "", "待测试的图片路径 (必填)")
		model     = flag.String("model", envOrDefault("OPENAI_MODEL", "gpt-4o-mini"), "模型 ID")
		baseURL   = flag.String("base", envOrDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"), "API Base URL")
		apiKey    = flag.String("key", os.Getenv("OPENAI_API_KEY"), "API Key (可通过 OPENAI_API_KEY 环境变量传入)")
		prompt    = flag.String("prompt", "请详细描述这张图片的内容，并输出 JSON。", "文本提示")
		detail    = flag.String("detail", "", "图像 detail 级别，可选 high/low/auto")
		maxTokens = flag.Int("max_tokens", 800, "最大返回 token 数")
		outDir    = flag.String("out", "logs", "日志输出目录")
	)
	flag.Parse()

	if *imagePath == "" {
		log.Fatalf("请通过 -image 指定图片路径")
	}
	if *apiKey == "" {
		log.Fatalf("请设置 OPENAI_API_KEY 或通过 -key 指定")
	}

	imgData, err := os.ReadFile(*imagePath)
	if err != nil {
		log.Fatalf("读取图片失败: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(imgData)
	dataURI := fmt.Sprintf("data:%s;base64,%s", detectMime(*imagePath), b64)

	reqBody := chatRequest{
		Model: *model,
		Messages: []chatMessage{
			{
				Role: "user",
				Content: []messagePart{
					{
						Type: "text",
						Text: *prompt,
					},
					{
						Type: "image_url",
						ImageURL: &messageImageURL{
							URL:    dataURI,
							Detail: *detail,
						},
					},
				},
			},
		},
		MaxTokens: *maxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		log.Fatalf("序列化请求失败: %v", err)
	}

	endpoint := strings.TrimRight(*baseURL, "/") + "/chat/completions"
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("读取响应失败: %v", err)
	}

	entry := logEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Request: requestEntry{
			Method: req.Method,
			URL:    endpoint,
			Headers: map[string]string{
				"Content-Type":  req.Header.Get("Content-Type"),
				"Authorization": "Bearer ***",
			},
			Body: json.RawMessage(bodyBytes),
		},
		Response: responseEntry{
			Status: resp.Status,
			Headers: map[string]string{
				"Content-Type": resp.Header.Get("Content-Type"),
				"X-Request-ID": resp.Header.Get("x-request-id"),
			},
			Body: string(respBody),
		},
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("创建日志目录失败: %v", err)
	}
	filename := filepath.Join(*outDir, fmt.Sprintf("api_test_%s.json", time.Now().Format("20060102_150405")))
	fileData, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		log.Fatalf("序列化日志失败: %v", err)
	}
	if err := os.WriteFile(filename, fileData, 0o644); err != nil {
		log.Fatalf("写入日志失败: %v", err)
	}

	fmt.Printf("测试完成，响应状态：%s\n", resp.Status)
	fmt.Printf("完整日志已保存：%s\n", filename)
	fmt.Printf("响应内容：\n%s\n", string(respBody))
}

func detectMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "image/png"
	}
	if m := mime.TypeByExtension(ext); m != "" {
		return m
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
