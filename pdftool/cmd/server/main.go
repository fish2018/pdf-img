package main

import (
	"log"

	"pdftool/internal/config"
	"pdftool/internal/httpserver"
	"pdftool/internal/service"
	"pdftool/internal/translator"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	defaultProvider := translator.ProviderConfig{
		Type:           translator.ProviderTypeOpenAI,
		BaseURL:        cfg.OpenAIBaseURL,
		APIKey:         cfg.OpenAIAPIKey,
		Model:          cfg.OpenAIModel,
		Timeout:        cfg.RequestTimeout,
		MaxTokens:      translator.SanitizeMaxTokens(0),
		OptimizeLayout: true,
	}

	taskSvc, err := service.NewTaskService(cfg.StorageDir, cfg.StaticPrefix, cfg.PDFFontPath, defaultProvider, cfg.MaxWorkers)
	if err != nil {
		log.Fatalf("初始化任务服务失败: %v", err)
	}

	server := httpserver.New(cfg, taskSvc)
	log.Printf("PDF tool service listening on %s", cfg.ListenAddr)
	if err := server.Run(); err != nil {
		log.Fatalf("服务异常退出: %v", err)
	}
}
