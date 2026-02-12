package httpserver

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"pdftool/internal/config"
	"pdftool/internal/service"
	"pdftool/internal/translator"
)

// Server wires HTTP handlers to the task service.
type Server struct {
	cfg     config.Config
	engine  *gin.Engine
	taskSvc *service.TaskService
}

// New builds the HTTP server.
func New(cfg config.Config, taskSvc *service.TaskService) *Server {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.MaxMultipartMemory = 128 << 20 // 128MB

	corsCfg := cors.DefaultConfig()
	corsCfg.AllowAllOrigins = true
	corsCfg.AllowHeaders = []string{"Origin", "Content-Type", "Authorization"}
	router.Use(cors.New(corsCfg))

	router.StaticFS(cfg.StaticPrefix, http.Dir(cfg.StorageDir))

	s := &Server{
		cfg:     cfg,
		engine:  router,
		taskSvc: taskSvc,
	}

	api := router.Group("/api/pdf")
	{
		api.GET("/tasks", s.handleListTasks)
		api.POST("/tasks", s.handleCreateTask)
		api.GET("/tasks/:taskID", s.handleGetTask)
		api.DELETE("/tasks/:taskID", s.handleDeleteTask)
		api.POST("/tasks/:taskID/pages/:pageNumber/retranslate", s.handleRetranslatePage)
		api.POST("/tasks/:taskID/layout", s.handleFormatTaskLayout)
		api.POST("/tasks/:taskID/export/txt", s.handleExportTxt)
		api.POST("/tasks/:taskID/export/pdf", s.handleExportPdf)
		api.POST("/providers/test", s.handleTestProvider)
		api.POST("/providers/models", s.handleFetchProviderModels)
	}

	return s
}

// Run starts the HTTP server.
func (s *Server) Run() error {
	return s.engine.Run(s.cfg.ListenAddr)
}

func (s *Server) handleCreateTask(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传PDF文件"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".pdf") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持PDF文件"})
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("读取上传文件失败: %v", err)})
		return
	}
	defer file.Close()

	apiType := c.PostForm("provider_api_type")
	if strings.TrimSpace(apiType) == "" {
		apiType = c.PostForm("provider_type")
	}
	maxTokens := parseOptionalInt(c.PostForm("provider_max_tokens"))
	provider := translator.ProviderConfig{
		Type:           translator.ProviderType(apiType),
		BaseURL:        strings.TrimSpace(c.PostForm("provider_base")),
		APIKey:         strings.TrimSpace(c.PostForm("provider_key")),
		Model:          strings.TrimSpace(c.PostForm("provider_model")),
		MaxTokens:      maxTokens,
		OptimizeLayout: true,
	}

	settings := service.TranslationSettings{
		RangeMode:   strings.TrimSpace(c.PostForm("initial_range_mode")),
		RangeCustom: parseOptionalInt(c.PostForm("initial_range_custom")),
		RangeStart:  parseOptionalInt(c.PostForm("initial_range_start")),
		RangeEnd:    parseOptionalInt(c.PostForm("initial_range_end")),
		BatchLimit:  parseOptionalInt(c.PostForm("initial_batch_limit")),
	}
	if settings.BatchLimit < 0 {
		settings.BatchLimit = 0
	}

	task, err := s.taskSvc.CreateTask(c.Request.Context(), file, fileHeader.Filename, provider, settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.taskSvc.ToResponse(task))
}

func (s *Server) handleListTasks(c *gin.Context) {
	tasks, err := s.taskSvc.ListTasks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (s *Server) handleGetTask(c *gin.Context) {
	taskID := c.Param("taskID")
	task, err := s.taskSvc.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.taskSvc.ToResponse(task))
}

func (s *Server) handleDeleteTask(c *gin.Context) {
	taskID := c.Param("taskID")
	if err := s.taskSvc.DeleteTask(taskID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (s *Server) handleRetranslatePage(c *gin.Context) {
	taskID := c.Param("taskID")
	pageNumber, err := strconv.Atoi(c.Param("pageNumber"))
	if err != nil || pageNumber <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "页码格式错误"})
		return
	}
	var req struct {
		ProviderType      string `json:"provider_type"`
		ProviderAPIType   string `json:"provider_api_type"`
		ProviderBase      string `json:"provider_base"`
		ProviderKey       string `json:"provider_key"`
		ProviderModel     string `json:"provider_model"`
		ProviderMaxTokens int    `json:"provider_max_tokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	apiType := req.ProviderAPIType
	if strings.TrimSpace(apiType) == "" {
		apiType = req.ProviderType
	}
	provider := translator.ProviderConfig{
		Type:           translator.ProviderType(apiType),
		BaseURL:        strings.TrimSpace(req.ProviderBase),
		APIKey:         strings.TrimSpace(req.ProviderKey),
		Model:          strings.TrimSpace(req.ProviderModel),
		MaxTokens:      req.ProviderMaxTokens,
		OptimizeLayout: true,
	}

	task, _, err := s.taskSvc.RetranslatePage(c.Request.Context(), taskID, pageNumber, provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.taskSvc.ToResponse(task))
}

func (s *Server) handleFormatTaskLayout(c *gin.Context) {
	taskID := c.Param("taskID")
	var req struct {
		ProviderType      string `json:"provider_type"`
		ProviderAPIType   string `json:"provider_api_type"`
		ProviderBase      string `json:"provider_base"`
		ProviderKey       string `json:"provider_key"`
		ProviderModel     string `json:"provider_model"`
		ProviderMaxTokens int    `json:"provider_max_tokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	apiType := req.ProviderAPIType
	if strings.TrimSpace(apiType) == "" {
		apiType = req.ProviderType
	}
	provider := translator.ProviderConfig{
		Type:           translator.ProviderType(apiType),
		BaseURL:        strings.TrimSpace(req.ProviderBase),
		APIKey:         strings.TrimSpace(req.ProviderKey),
		Model:          strings.TrimSpace(req.ProviderModel),
		MaxTokens:      req.ProviderMaxTokens,
		OptimizeLayout: true,
	}
	task, url, err := s.taskSvc.FormatTaskLayout(c.Request.Context(), taskID, provider)
	if err != nil {
		log.Printf("format task %s failed: %v", taskID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task": s.taskSvc.ToResponse(task),
		"url":  url,
	})
}

func (s *Server) handleExportTxt(c *gin.Context) {
	taskID := c.Param("taskID")
	variant := strings.ToLower(strings.TrimSpace(c.Query("variant")))
	if variant == "" {
		variant = "original"
	}
	if variant == "formatted" {
		task, err := s.taskSvc.GetTask(taskID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if !task.FormattedByAI || strings.TrimSpace(task.FormattedTxtURL) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "尚未生成 AI 排版版本"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"task": s.taskSvc.ToResponse(task),
			"url":  task.FormattedTxtURL,
		})
		return
	}
	task, url, err := s.taskSvc.MergeText(taskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task": s.taskSvc.ToResponse(task),
		"url":  url,
	})
}

func (s *Server) handleExportPdf(c *gin.Context) {
	taskID := c.Param("taskID")
	task, url, err := s.taskSvc.MergePDF(taskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task": s.taskSvc.ToResponse(task),
		"url":  url,
	})
}

func (s *Server) handleTestProvider(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		Model   string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 不能为空"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "连接测试成功（模拟）",
	})
}

func (s *Server) handleFetchProviderModels(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	models := sampleModels(strings.ToLower(strings.TrimSpace(req.Type)))
	c.JSON(http.StatusOK, gin.H{
		"models": models,
	})
}

func sampleModels(providerType string) []map[string]string {
	switch providerType {
	case "gemini":
		return []map[string]string{
			{"id": "gemini-1.5-flash", "name": "Gemini Flash", "apiType": "gemini"},
			{"id": "gemini-1.5-pro", "name": "Gemini Pro", "apiType": "gemini"},
		}
	case "anthropic":
		return []map[string]string{
			{"id": "claude-3-5-sonnet", "name": "Claude 3.5 Sonnet", "apiType": "anthropic"},
			{"id": "claude-3-opus", "name": "Claude 3 Opus", "apiType": "anthropic"},
		}
	default:
		return []map[string]string{
			{"id": "gpt-4o-mini", "name": "GPT-4o Mini", "apiType": "openai"},
			{"id": "gpt-4o", "name": "GPT-4o", "apiType": "openai"},
			{"id": "gpt-4.1-mini", "name": "GPT-4.1 Mini", "apiType": "openai"},
		}
	}
}

func parseOptionalInt(value string) int {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return v
}
