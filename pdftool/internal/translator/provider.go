package translator

import (
	"strings"
	"time"
)

// ProviderType enumerates supported AI providers.
type ProviderType string

const (
	ProviderTypeOpenAI    ProviderType = "openai"
	ProviderTypeGemini    ProviderType = "gemini"
	ProviderTypeAnthropic ProviderType = "anthropic"
)

// ProviderConfig describes runtime translator configuration.
type ProviderConfig struct {
	Type           ProviderType
	BaseURL        string
	APIKey         string
	Model          string
	Timeout        time.Duration
	MaxTokens      int
	OptimizeLayout bool
}

// OpenAIConfig is kept for backwards compatibility.
type OpenAIConfig = ProviderConfig

// NormalizeProviderType coerces user inputs to known types.
func NormalizeProviderType(value string) ProviderType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gemini":
		return ProviderTypeGemini
	case "anthropic":
		return ProviderTypeAnthropic
	default:
		return ProviderTypeOpenAI
	}
}

// NewTranslator builds a translator according to provider type.
func NewTranslator(cfg ProviderConfig) (Translator, error) {
	cfg.Type = NormalizeProviderType(string(cfg.Type))
	cfg.MaxTokens = SanitizeMaxTokens(cfg.MaxTokens)
	switch cfg.Type {
	case ProviderTypeGemini:
		return newGeminiTranslator(cfg)
	case ProviderTypeAnthropic:
		return newAnthropicTranslator(cfg)
	default:
		return newOpenAITranslator(cfg)
	}
}

// NewOpenAITranslator keeps the old API available.
func NewOpenAITranslator(cfg ProviderConfig) (Translator, error) {
	cfg.Type = ProviderTypeOpenAI
	cfg.MaxTokens = SanitizeMaxTokens(cfg.MaxTokens)
	return newOpenAITranslator(cfg)
}

// SanitizeMaxTokens ensures a reasonable default.
func SanitizeMaxTokens(val int) int {
	if val <= 0 {
		return 8192
	}
	return val
}
