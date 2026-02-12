package service

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jung-kurt/gofpdf"
	"golang.org/x/text/encoding/simplifiedchinese"

	"pdftool/internal/assets"
	"pdftool/internal/model"
	"pdftool/internal/pdfutil"
	"pdftool/internal/translator"
)

// TaskService coordinates PDF processing and persistence.
type TaskService struct {
	storageDir      string
	staticPrefix    string
	fontPath        string
	maxWorkers      int
	defaultProvider translator.ProviderConfig
	mu              sync.Mutex
}

// TranslationSettings controls initial translation behavior.
type TranslationSettings struct {
	RangeMode   string
	RangeCustom int
	RangeStart  int
	RangeEnd    int
	BatchLimit  int
}

// NewTaskService constructs the coordinator.
func NewTaskService(storageDir, staticPrefix, fontPath string, defaultProvider translator.ProviderConfig, maxWorkers int) (*TaskService, error) {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return nil, err
	}
	if defaultProvider.Timeout == 0 {
		defaultProvider.Timeout = 90 * time.Second
	}
	defaultProvider.MaxTokens = translator.SanitizeMaxTokens(defaultProvider.MaxTokens)
	return &TaskService{
		storageDir:      storageDir,
		staticPrefix:    staticPrefix,
		fontPath:        fontPath,
		maxWorkers:      maxWorkers,
		defaultProvider: defaultProvider,
	}, nil
}

// CreateTask reads the uploaded PDF, extracts the pages, and translates them.
func (s *TaskService) CreateTask(ctx context.Context, reader io.Reader, fileName string, provider translator.ProviderConfig, settings TranslationSettings) (*model.Task, error) {
	if reader == nil {
		return nil, fmt.Errorf("missing file reader")
	}
	providerCfg, err := s.mergeProviderConfig(provider, nil)
	if err != nil {
		return nil, err
	}
	providerCfg.OptimizeLayout = true
	translatorClient, err := translator.NewTranslator(providerCfg)
	if err != nil {
		return nil, err
	}
	taskID := uuid.NewString()
	taskDir := s.taskDir(taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return nil, fmt.Errorf("create task dir: %w", err)
	}

	safeName := sanitizeName(fileName)
	if safeName == "" {
		safeName = "document.pdf"
	}

	sourcePath := filepath.Join(taskDir, "source.pdf")
	outFile, err := os.Create(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("create source file: %w", err)
	}
	if _, err := io.Copy(outFile, reader); err != nil {
		outFile.Close()
		return nil, fmt.Errorf("write source file: %w", err)
	}
	outFile.Close()

	pagesDir := filepath.Join(taskDir, "pages")
	imagePaths, err := pdfutil.RenderPages(sourcePath, pagesDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	task := &model.Task{
		ID:           taskID,
		FileName:     safeName,
		OriginalPath: sourcePath,
		TotalPages:   len(imagePaths),
		Pages:        make([]*model.PageResult, 0, len(imagePaths)),
		CreatedAt:    now,
		UpdatedAt:    now,
		Provider: model.ProviderInfo{
			Type:      string(providerCfg.Type),
			BaseURL:   providerCfg.BaseURL,
			Model:     providerCfg.Model,
			MaxTokens: providerCfg.MaxTokens,
		},
		FormattingOptimized: true,
	}

	for idx, imgPath := range imagePaths {
		base := filepath.Base(imgPath)
		textFile := replaceExt(base, ".txt")
		page := &model.PageResult{
			ID:         uuid.NewString(),
			PageNumber: idx + 1,
			ImagePath:  imgPath,
			ImageURL:   s.buildFileURL(task.ID, "pages", base),
			TextPath:   filepath.Join(pagesDir, textFile),
			Status:     model.PageStatusPending,
			UpdatedAt:  now,
		}
		task.Pages = append(task.Pages, page)
	}

	selectedMap := determineInitialPageSet(len(task.Pages), settings)
	var selectedPages []*model.PageResult
	now = time.Now()
	for _, page := range task.Pages {
		if selectedMap[page.PageNumber] {
			selectedPages = append(selectedPages, page)
			continue
		}
		page.Status = model.PageStatusCompleted
		page.HasText = false
		page.SourceText = ""
		page.Translation = ""
		page.Error = ""
		page.UpdatedAt = now
	}

	if err := s.saveTask(task); err != nil {
		return nil, err
	}
	go s.translateTaskPages(context.Background(), task, selectedPages, translatorClient, settings.BatchLimit)
	return task, nil
}

// GetTask loads a task by ID.
func (s *TaskService) GetTask(taskID string) (*model.Task, error) {
	return s.loadTask(taskID)
}

// RetranslatePage re-runs translation for a specific page.
func (s *TaskService) RetranslatePage(ctx context.Context, taskID string, pageNumber int, provider translator.ProviderConfig) (*model.Task, *model.PageResult, error) {
	task, err := s.loadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	providerCfg, err := s.mergeProviderConfig(provider, task)
	if err != nil {
		return nil, nil, err
	}
	translatorClient, err := translator.NewTranslator(providerCfg)
	if err != nil {
		return nil, nil, err
	}
	task.Provider = model.ProviderInfo{
		Type:      string(providerCfg.Type),
		BaseURL:   providerCfg.BaseURL,
		Model:     providerCfg.Model,
		MaxTokens: providerCfg.MaxTokens,
	}
	if err := s.saveTask(task); err != nil {
		return nil, nil, err
	}
	var target *model.PageResult
	for _, page := range task.Pages {
		if page.PageNumber == pageNumber {
			target = page
			break
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("page %d not found", pageNumber)
	}
	if err := s.translateSinglePage(ctx, task, target, translatorClient, true); err != nil {
		return nil, nil, err
	}
	updatedTask, err := s.loadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	var updatedPage *model.PageResult
	for _, page := range updatedTask.Pages {
		if page.PageNumber == pageNumber {
			updatedPage = page
			break
		}
	}
	return updatedTask, updatedPage, nil
}

// MergeText generates a concatenated TXT document from translated pages.
func (s *TaskService) MergeText(taskID string) (*model.Task, string, error) {
	task, err := s.loadTask(taskID)
	if err != nil {
		return nil, "", err
	}

	combinedText, err := s.buildCombinedText(task)
	if err != nil {
		return nil, "", err
	}
	combinedPath := filepath.Join(s.taskDir(task.ID), "combined.txt")
	if err := os.WriteFile(combinedPath, []byte(combinedText), 0o644); err != nil {
		return nil, "", fmt.Errorf("写入TXT失败: %w", err)
	}

	task.CombinedTxtPath = combinedPath
	task.CombinedTxtURL = s.buildFileURL(task.ID, "combined.txt")
	if err := s.saveTask(task); err != nil {
		return nil, "", err
	}
	return task, task.CombinedTxtURL, nil
}

func (s *TaskService) buildCombinedText(task *model.Task) (string, error) {
	var builder strings.Builder
	for _, page := range task.Pages {
		if !page.HasText {
			continue
		}
		text := strings.TrimSpace(page.Translation)
		if text == "" {
			continue
		}
		builder.WriteString(fmt.Sprintf("第%d页\n", page.PageNumber))
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	if builder.Len() == 0 {
		return "", fmt.Errorf("没有可用的翻译文本")
	}
	return builder.String(), nil
}

// MergePDF generates a single PDF that contains translated text or original images.
func (s *TaskService) MergePDF(taskID string) (*model.Task, string, error) {
	task, err := s.loadTask(taskID)
	if err != nil {
		return nil, "", err
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	fontFamily := s.prepareFont(pdf)
	for _, page := range task.Pages {
		pdf.AddPage()
		s.setFont(pdf, fontFamily, 12)
		header := s.encodeText(pdf, fontFamily, fmt.Sprintf("第%d页", page.PageNumber))
		pdf.MultiCell(0, 6, header, "", "L", false)
		pdf.Ln(2)

		text := strings.TrimSpace(page.Translation)
		if page.HasText && text != "" {
			s.setFont(pdf, fontFamily, 11)
			pdf.MultiCell(0, 6, s.encodeText(pdf, fontFamily, text), "", "L", false)
			continue
		}

		ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(page.ImagePath)), ".")
		if ext == "" {
			ext = "PNG"
		}
		opt := gofpdf.ImageOptions{
			ImageType: ext,
			ReadDpi:   true,
		}
		pageWidth, pageHeight := pdf.GetPageSize()
		margin := 10.0
		availW := pageWidth - margin*2
		availH := pageHeight - margin*2
		displayW, displayH := fitImage(page.ImagePath, availW, availH)
		if displayW == 0 || displayH == 0 {
			displayW = availW
			displayH = availH
		}
		pdf.ImageOptions(page.ImagePath, margin, margin, displayW, displayH, false, opt, 0, "")
		if err := pdf.Error(); err != nil {
			log.Printf("embed image failed (page %d): %v", page.PageNumber, err)
			pdf.ClearError()
			pdf.MultiCell(0, 6, "【无法插入原图】", "", "L", false)
		}
	}

	combinedPath := filepath.Join(s.taskDir(task.ID), "combined.pdf")
	if err := pdf.OutputFileAndClose(combinedPath); err != nil {
		return nil, "", fmt.Errorf("生成PDF失败: %w", err)
	}

	task.CombinedPDFPath = combinedPath
	task.CombinedPDFURL = s.buildFileURL(task.ID, "combined.pdf")
	if err := s.saveTask(task); err != nil {
		return nil, "", err
	}
	return task, task.CombinedPDFURL, nil
}

const (
	formatterChunkSize = 60 * 1024 // 60KB per chunk upper bound
	minFormatterChunk  = 12 * 1024
)

// FormatTaskLayout uses an AI formatter to optimize the combined text layout.
func (s *TaskService) FormatTaskLayout(ctx context.Context, taskID string, provider translator.ProviderConfig) (*model.Task, string, error) {
	task, err := s.loadTask(taskID)
	if err != nil {
		return nil, "", err
	}
	log.Printf("start AI layout task=%s model=%s", task.ID, provider.Model)
	providerCfg, err := s.mergeProviderConfig(provider, task)
	if err != nil {
		return nil, "", err
	}
	formatter, err := translator.NewFormatter(providerCfg)
	if err != nil {
		return nil, "", err
	}
	baseText, err := s.buildCombinedText(task)
	if err != nil {
		return nil, "", err
	}
	chunkSize := estimateFormatterChunkSize(providerCfg.Type, providerCfg.MaxTokens)
	chunks, err := s.prepareFormatterChunks(task, baseText, chunkSize)
	if err != nil {
		return nil, "", err
	}
	totalChunks := len(chunks)
	if err := s.updateFormattingState(task.ID, func(t *model.Task) {
		t.FormattingInProgress = true
		t.FormattingTotalChunks = totalChunks
		t.FormattingCompletedChunks = 0
	}); err != nil {
		return nil, "", err
	}
	results := make([]string, len(chunks))
	chunkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	workerLimit := 3
	if len(chunks) < workerLimit {
		workerLimit = len(chunks)
	}
	var currentLimit int32 = int32(workerLimit)
	var activeSlots int32
	acquireSlot := func() bool {
		for {
			if chunkCtx.Err() != nil {
				return false
			}
			curLimit := atomic.LoadInt32(&currentLimit)
			curActive := atomic.LoadInt32(&activeSlots)
			if curActive < curLimit {
				if atomic.CompareAndSwapInt32(&activeSlots, curActive, curActive+1) {
					return true
				}
			} else {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	releaseSlot := func() {
		atomic.AddInt32(&activeSlots, -1)
	}

	var mu sync.Mutex
	var firstErr error
	var firstErrSet bool
	setError := func(err error) {
		mu.Lock()
		if !firstErrSet {
			firstErr = err
			firstErrSet = true
			cancel()
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	var completedChunks int32
	successful := false
	defer func() {
		if successful || totalChunks == 0 {
			return
		}
		progress := int(atomic.LoadInt32(&completedChunks))
		if err := s.updateFormattingState(task.ID, func(t *model.Task) {
			t.FormattingInProgress = false
			if t.FormattingTotalChunks == 0 {
				t.FormattingTotalChunks = totalChunks
			}
			t.FormattingCompletedChunks = progress
		}); err != nil {
			log.Printf("failed to finalize AI 排版进度(%s): %v", task.ID, err)
		}
	}()

	processChunk := func(idx int, chunk translator.FormatterChunk) {
		defer wg.Done()
		retries := 0
		for {
			select {
			case <-chunkCtx.Done():
				return
			default:
			}
			if !acquireSlot() {
				return
			}
			log.Printf("format chunk %d/%d file=%s size=%d bytes", idx+1, len(chunks), chunk.FileName, len(chunk.Data))
			result, err := formatter.Format(chunkCtx, chunk, idx+1)
			releaseSlot()
			if err != nil {
				if formatterIsRateLimit(err) && retries < 3 {
					if atomic.LoadInt32(&currentLimit) > 1 {
						log.Printf("chunk %d hit rate limit, lowering concurrency to 1", idx+1)
						atomic.StoreInt32(&currentLimit, 1)
					}
					retries++
					time.Sleep(time.Duration(retries) * time.Second)
					continue
				}
				setError(err)
				return
			}
			clean := strings.TrimSpace(result)
			srcLen := len([]rune(string(chunk.Data)))
			if srcLen > 0 && len([]rune(clean)) < srcLen/2 {
				setError(fmt.Errorf("AI 排版 chunk %d 返回内容过短，可能被截断", idx+1))
				return
			}
			results[idx] = clean
			completed := int(atomic.AddInt32(&completedChunks, 1))
			if err := s.updateFormattingState(task.ID, func(t *model.Task) {
				t.FormattingInProgress = true
				if t.FormattingTotalChunks == 0 {
					t.FormattingTotalChunks = totalChunks
				}
				t.FormattingCompletedChunks = completed
			}); err != nil {
				log.Printf("failed to update AI 排版进度(%s): %v", task.ID, err)
			}
			log.Printf("chunk %d completed, output %d chars", idx+1, len([]rune(clean)))
			return
		}
	}

	for idx, chunk := range chunks {
		wg.Add(1)
		go processChunk(idx, chunk)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, "", firstErr
	}

	formatted := strings.TrimSpace(strings.Join(results, "\n\n"))
	if formatted == "" {
		return nil, "", fmt.Errorf("AI 排版失败，返回内容为空")
	}
	formattedPath := filepath.Join(s.taskDir(task.ID), "formatted.txt")
	if err := os.WriteFile(formattedPath, []byte(formatted), 0o644); err != nil {
		return nil, "", fmt.Errorf("写入AI排版TXT失败: %w", err)
	}
	if task, err = s.loadTask(task.ID); err != nil {
		return nil, "", err
	}
	task.FormattedByAI = true
	task.FormattedTxtPath = formattedPath
	task.FormattedTxtURL = s.buildFileURL(task.ID, "formatted.txt")
	task.FormattingInProgress = false
	task.FormattingTotalChunks = totalChunks
	task.FormattingCompletedChunks = totalChunks
	if err := s.saveTask(task); err != nil {
		return nil, "", err
	}
	atomic.StoreInt32(&completedChunks, int32(totalChunks))
	successful = true
	log.Printf("AI layout finished task=%s formattedTxt=%s", task.ID, task.FormattedTxtURL)
	return task, task.FormattedTxtURL, nil
}

func (s *TaskService) updateFormattingState(taskID string, mutate func(*model.Task)) error {
	if mutate == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.loadTask(taskID)
	if err != nil {
		return err
	}
	mutate(task)
	return s.saveTaskLocked(task)
}

func (s *TaskService) prepareFormatterChunks(task *model.Task, text string, chunkSize int) ([]translator.FormatterChunk, error) {
	chunkStrings := splitTextChunks(text, chunkSize)
	if len(chunkStrings) == 0 {
		return nil, fmt.Errorf("没有可排版的文本内容")
	}
	chunkDir := filepath.Join(s.taskDir(task.ID), "formatter_chunks")
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建排版临时目录失败: %w", err)
	}
	log.Printf("prepared %d chunks total=%d bytes chunkSize=%d", len(chunkStrings), len(text), chunkSize)
	chunks := make([]translator.FormatterChunk, 0, len(chunkStrings))
	for idx, content := range chunkStrings {
		fileName := fmt.Sprintf("chunk-%03d.txt", idx+1)
		data := []byte(content)
		path := filepath.Join(chunkDir, fileName)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("写入排版临时文件失败: %w", err)
		}
		log.Printf("prepared formatter chunk %s size=%d bytes", path, len(data))
		chunks = append(chunks, translator.FormatterChunk{
			FileName: fileName,
			MimeType: "text/plain",
			Data:     data,
		})
	}
	return chunks, nil
}

// ToResponse converts an internal task to API payload.
func (s *TaskService) ToResponse(task *model.Task) *model.TaskResponse {
	resp := &model.TaskResponse{
		ID:                        task.ID,
		FileName:                  task.FileName,
		TotalPages:                task.TotalPages,
		CreatedAt:                 task.CreatedAt,
		UpdatedAt:                 task.UpdatedAt,
		CombinedTxtURL:            task.CombinedTxtURL,
		CombinedPDFURL:            task.CombinedPDFURL,
		FormattedTxtURL:           task.FormattedTxtURL,
		Provider:                  task.Provider,
		Pages:                     make([]*model.PageResponse, 0, len(task.Pages)),
		FormattingOptimized:       task.FormattingOptimized,
		FormattedByAI:             task.FormattedByAI,
		FormattingInProgress:      task.FormattingInProgress,
		FormattingTotalChunks:     task.FormattingTotalChunks,
		FormattingCompletedChunks: task.FormattingCompletedChunks,
	}
	for _, page := range task.Pages {
		resp.Pages = append(resp.Pages, &model.PageResponse{
			ID:          page.ID,
			PageNumber:  page.PageNumber,
			ImageURL:    page.ImageURL,
			TextURL:     page.TextURL,
			HasText:     page.HasText,
			SourceText:  page.SourceText,
			Translation: page.Translation,
			Status:      page.Status,
			Error:       page.Error,
			UpdatedAt:   page.UpdatedAt,
		})
	}
	return resp
}

func (s *TaskService) translateTaskPages(ctx context.Context, task *model.Task, pages []*model.PageResult, translatorClient translator.Translator, batchLimit int) {
	if translatorClient == nil || len(pages) == 0 {
		log.Printf("translator is nil, skip translation task %s", task.ID)
		return
	}
	workerCount := s.maxWorkers
	if batchLimit > 0 && workerCount > batchLimit {
		workerCount = batchLimit
	}
	if workerCount > len(pages) {
		workerCount = len(pages)
	}
	if workerCount == 0 {
		return
	}
	jobs := make(chan *model.PageResult)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for page := range jobs {
				if err := s.translateSinglePage(ctx, task, page, translatorClient, false); err != nil {
					log.Printf("translate page %d failed: %v", page.PageNumber, err)
				}
			}
		}()
	}
	for _, page := range pages {
		jobs <- page
	}
	close(jobs)
	wg.Wait()
}

func (s *TaskService) translateSinglePage(ctx context.Context, task *model.Task, page *model.PageResult, translatorClient translator.Translator, mergeOnSave bool) error {
	ctxWithPage := translator.WithPageNumber(ctx, page.PageNumber)
	result, err := translatorClient.Translate(ctxWithPage, page.ImagePath)
	if err != nil {
		page.Status = model.PageStatusError
		page.Error = err.Error()
		page.UpdatedAt = time.Now()
		return s.saveTask(task)
	}

	page.HasText = result.HasText
	page.SourceText = strings.TrimSpace(result.SourceText)
	page.Translation = strings.TrimSpace(result.TranslatedText)
	page.Error = ""

	if page.HasText && page.Translation != "" {
		if err := os.WriteFile(page.TextPath, []byte(page.Translation), 0o644); err != nil {
			page.Status = model.PageStatusError
			page.Error = fmt.Sprintf("写入TXT失败: %v", err)
			page.UpdatedAt = time.Now()
			return s.saveTask(task)
		}
		page.TextURL = s.buildFileURL(task.ID, "pages", filepath.Base(page.TextPath))
	} else {
		os.Remove(page.TextPath)
		page.TextURL = ""
	}

	page.Status = model.PageStatusCompleted
	page.UpdatedAt = time.Now()
	return s.persistPageUpdate(task, page, mergeOnSave)
}

func (s *TaskService) persistPageUpdate(task *model.Task, page *model.PageResult, merge bool) error {
	if !merge {
		return s.saveTask(task)
	}
	current, err := s.loadTask(task.ID)
	if err != nil {
		return err
	}
	replaced := false
	for idx, existing := range current.Pages {
		if existing.ID == page.ID {
			current.Pages[idx] = page
			replaced = true
			break
		}
	}
	if !replaced {
		current.Pages = append(current.Pages, page)
	}
	return s.saveTask(current)
}

func (s *TaskService) loadTask(taskID string) (*model.Task, error) {
	metaPath := filepath.Join(s.taskDir(taskID), "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("读取任务失败: %w", err)
	}
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("解析任务失败: %w", err)
	}
	return &task, nil
}

func (s *TaskService) saveTask(task *model.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveTaskLocked(task)
}

func (s *TaskService) saveTaskLocked(task *model.Task) error {
	task.UpdatedAt = time.Now()
	metaPath := filepath.Join(s.taskDir(task.ID), "meta.json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, metaPath)
}

func (s *TaskService) taskDir(taskID string) string {
	return filepath.Join(s.storageDir, taskID)
}

func (s *TaskService) buildFileURL(taskID string, parts ...string) string {
	segments := []string{taskID}
	for _, p := range parts {
		segments = append(segments, filepath.ToSlash(p))
	}
	rel := path.Join(segments...)
	return path.Join(s.staticPrefix, rel)
}

// ListTasks returns lightweight summaries for all stored tasks.
func (s *TaskService) ListTasks() ([]*model.TaskSummary, error) {
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		return nil, fmt.Errorf("读取任务目录失败: %w", err)
	}
	summaries := make([]*model.TaskSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		task, err := s.loadTask(taskID)
		if err != nil {
			log.Printf("skip task %s: %v", taskID, err)
			continue
		}
		summaries = append(summaries, summarizeTask(task))
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt.Equal(summaries[j].UpdatedAt) {
			return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
		}
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}

// DeleteTask removes all files associated with a task.
func (s *TaskService) DeleteTask(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("缺少任务 ID")
	}
	taskDir := s.taskDir(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(taskDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("任务不存在")
		}
		return fmt.Errorf("删除任务失败: %w", err)
	}
	if err := os.RemoveAll(taskDir); err != nil {
		return fmt.Errorf("删除任务失败: %w", err)
	}
	return nil
}

func summarizeTask(task *model.Task) *model.TaskSummary {
	var completed, pending, failed int
	for _, page := range task.Pages {
		switch page.Status {
		case model.PageStatusCompleted:
			completed++
		case model.PageStatusError:
			failed++
		default:
			pending++
		}
	}
	return &model.TaskSummary{
		ID:             task.ID,
		FileName:       task.FileName,
		TotalPages:     task.TotalPages,
		CompletedPages: completed,
		PendingPages:   pending,
		ErrorPages:     failed,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func replaceExt(name, ext string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + ext
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return filepath.Base(name)
}

func fitImage(path string, maxW, maxH float64) (float64, float64) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	cfg, err := png.DecodeConfig(file)
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return 0, 0
	}
	scale := math.Min(maxW/float64(cfg.Width), maxH/float64(cfg.Height))
	if !math.IsInf(scale, 0) && !math.IsNaN(scale) && scale > 0 {
		return float64(cfg.Width) * scale, float64(cfg.Height) * scale
	}
	return maxW, maxH
}

func (s *TaskService) mergeProviderConfig(input translator.ProviderConfig, task *model.Task) (translator.ProviderConfig, error) {
	cfg := s.defaultProvider
	if task != nil {
		if strings.TrimSpace(task.Provider.Type) != "" {
			cfg.Type = translator.NormalizeProviderType(task.Provider.Type)
		}
		if strings.TrimSpace(task.Provider.BaseURL) != "" {
			cfg.BaseURL = task.Provider.BaseURL
		}
		if strings.TrimSpace(task.Provider.Model) != "" {
			cfg.Model = task.Provider.Model
		}
		if task.Provider.MaxTokens > 0 {
			cfg.MaxTokens = task.Provider.MaxTokens
		}
	}
	if strings.TrimSpace(string(input.Type)) != "" {
		cfg.Type = translator.NormalizeProviderType(string(input.Type))
	}
	if strings.TrimSpace(input.BaseURL) != "" {
		cfg.BaseURL = strings.TrimSpace(input.BaseURL)
	}
	if strings.TrimSpace(input.Model) != "" {
		cfg.Model = strings.TrimSpace(input.Model)
	}
	if strings.TrimSpace(input.APIKey) != "" {
		cfg.APIKey = strings.TrimSpace(input.APIKey)
	}
	if input.MaxTokens > 0 {
		cfg.MaxTokens = input.MaxTokens
	}
	cfg.OptimizeLayout = true
	if input.Timeout > 0 {
		cfg.Timeout = input.Timeout
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300 * time.Second
	}
	cfg.Type = translator.NormalizeProviderType(string(cfg.Type))
	cfg.MaxTokens = translator.SanitizeMaxTokens(cfg.MaxTokens)
	if strings.TrimSpace(cfg.APIKey) == "" {
		return cfg, fmt.Errorf("缺少 API Key")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return cfg, fmt.Errorf("缺少模型 ID")
	}
	return cfg, nil
}

func splitTextChunks(text string, maxBytes int) []string {
	if maxBytes <= 0 {
		maxBytes = formatterChunkSize
	}
	var chunks []string
	var builder strings.Builder
	current := 0
	for _, r := range text {
		buf := make([]byte, utf8.RuneLen(r))
		utf8.EncodeRune(buf, r)
		if current+len(buf) > maxBytes && builder.Len() > 0 {
			chunks = append(chunks, builder.String())
			builder.Reset()
			current = 0
		}
		builder.Write(buf)
		current += len(buf)
		if r == '\n' && current > maxBytes-512 {
			chunks = append(chunks, builder.String())
			builder.Reset()
			current = 0
		}
	}
	if builder.Len() > 0 {
		chunks = append(chunks, builder.String())
	}
	return chunks
}

func estimateFormatterChunkSize(provider translator.ProviderType, maxTokens int) int {
	size := formatterChunkSize
	if maxTokens > 0 {
		estimated := int(float64(maxTokens) * 4 * 0.4)
		if estimated < minFormatterChunk {
			estimated = minFormatterChunk
		}
		if estimated > formatterChunkSize {
			estimated = formatterChunkSize
		}
		if estimated < size {
			size = estimated
		}
	}
	if provider == translator.ProviderTypeOpenAI {
		if size > minFormatterChunk*2 {
			size = size / 2
		} else {
			size = minFormatterChunk
		}
	}
	if size < minFormatterChunk {
		size = minFormatterChunk
	}
	return size
}

func formatterIsRateLimit(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") || strings.Contains(msg, "503") ||
		strings.Contains(msg, "rate limit") || strings.Contains(msg, "concurrency") ||
		strings.Contains(msg, "provider_error")
}

func determineInitialPageSet(total int, settings TranslationSettings) map[int]bool {
	result := make(map[int]bool)
	mode := strings.ToLower(strings.TrimSpace(settings.RangeMode))
	switch mode {
	case "custom":
		limit := settings.RangeCustom
		if limit <= 0 {
			break
		}
		if limit > total {
			limit = total
		}
		for i := 1; i <= limit; i++ {
			result[i] = true
		}
	case "range":
		start := settings.RangeStart
		end := settings.RangeEnd
		if start <= 0 && end <= 0 {
			break
		}
		if start <= 0 {
			start = 1
		}
		if end <= 0 {
			end = total
		}
		if start > end {
			start, end = end, start
		}
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}
		for i := start; i <= end; i++ {
			result[i] = true
		}
	default:
	}
	if len(result) == 0 {
		for i := 1; i <= total; i++ {
			result[i] = true
		}
	}
	return result
}

func (s *TaskService) prepareFont(pdf *gofpdf.Fpdf) string {
	fontPath := strings.TrimSpace(s.fontPath)
	if fontPath == "" {
		if data := assets.DefaultChineseFont(); len(data) > 0 {
			fontName := "embedded_cn"
			pdf.AddUTF8FontFromBytes(fontName, "", data)
			if err := pdf.Error(); err != nil {
				log.Printf("加载内置字体失败，将退回默认字体: %v", err)
				pdf.ClearError()
				return ""
			}
			return fontName
		}
		return ""
	}
	fontName := "custom_cn"
	pdf.AddUTF8Font(fontName, "", fontPath)
	if err := pdf.Error(); err != nil {
		log.Printf("加载 PDF 字体失败，将退回默认字体: %v", err)
		pdf.ClearError()
		if data := assets.DefaultChineseFont(); len(data) > 0 {
			fallbackName := "embedded_cn"
			pdf.AddUTF8FontFromBytes(fallbackName, "", data)
			if err := pdf.Error(); err != nil {
				log.Printf("加载内置字体失败，将退回默认字体: %v", err)
				pdf.ClearError()
				return ""
			}
			return fallbackName
		}
		return ""
	}
	return fontName
}

func (s *TaskService) setFont(pdf *gofpdf.Fpdf, family string, size float64) {
	if family != "" {
		pdf.SetFont(family, "", size)
		return
	}
	pdf.SetFont("Helvetica", "", size)
}

func (s *TaskService) encodeText(pdf *gofpdf.Fpdf, fontFamily, text string) string {
	if fontFamily != "" {
		return text
	}
	if translator := pdf.UnicodeTranslatorFromDescriptor(""); translator != nil {
		return translator(text)
	}
	if encoded, err := simplifiedchinese.GBK.NewEncoder().String(text); err == nil {
		return encoded
	}
	return text
}
