package model

import "time"

// PageStatus enumerates translation states for individual pages.
type PageStatus string

const (
	PageStatusPending   PageStatus = "pending"
	PageStatusCompleted PageStatus = "completed"
	PageStatusError     PageStatus = "error"
)

// PageResult tracks outputs for a rendered PDF page.
type PageResult struct {
	ID          string     `json:"id"`
	PageNumber  int        `json:"page_number"`
	ImagePath   string     `json:"image_path"`
	ImageURL    string     `json:"image_url"`
	TextPath    string     `json:"text_path"`
	TextURL     string     `json:"text_url"`
	HasText     bool       `json:"has_text"`
	SourceText  string     `json:"source_text"`
	Translation string     `json:"translation"`
	Status      PageStatus `json:"status"`
	Error       string     `json:"error"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Task aggregates all processing artifacts for a PDF.
type Task struct {
	ID                  string        `json:"id"`
	FileName            string        `json:"file_name"`
	OriginalPath        string        `json:"original_path"`
	TotalPages          int           `json:"total_pages"`
	Pages               []*PageResult `json:"pages"`
	CombinedTxtPath     string        `json:"combined_txt_path"`
	CombinedTxtURL      string        `json:"combined_txt_url"`
	CombinedPDFPath     string        `json:"combined_pdf_path"`
	CombinedPDFURL      string        `json:"combined_pdf_url"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	Provider            ProviderInfo  `json:"provider"`
	FormattingOptimized bool          `json:"formatting_optimized"`
	FormattedByAI       bool          `json:"formatted_by_ai"`
	FormattedTxtPath    string        `json:"formatted_txt_path"`
	FormattedTxtURL     string        `json:"formatted_txt_url"`
	FormattedPDFPath    string        `json:"formatted_pdf_path"`
	FormattedPDFURL     string        `json:"formatted_pdf_url"`
	FormattingInProgress bool         `json:"formatting_in_progress"`
	FormattingTotalChunks int         `json:"formatting_total_chunks"`
	FormattingCompletedChunks int     `json:"formatting_completed_chunks"`
}

// ProviderInfo keeps track of non-sensitive provider data.
type ProviderInfo struct {
	Type      string `json:"type"`
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	MaxTokens int    `json:"maxTokens"`
}

// PageResponse exposes sanitized page information to the frontend.
type PageResponse struct {
	ID          string     `json:"id"`
	PageNumber  int        `json:"pageNumber"`
	ImageURL    string     `json:"imageUrl"`
	TextURL     string     `json:"textUrl,omitempty"`
	HasText     bool       `json:"hasText"`
	SourceText  string     `json:"sourceText"`
	Translation string     `json:"translation"`
	Status      PageStatus `json:"status"`
	Error       string     `json:"error,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// TaskResponse is returned by the API.
type TaskResponse struct {
	ID                  string          `json:"id"`
	FileName            string          `json:"fileName"`
	TotalPages          int             `json:"totalPages"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
	CombinedTxtURL      string          `json:"combinedTxtUrl,omitempty"`
	CombinedPDFURL      string          `json:"combinedPdfUrl,omitempty"`
	FormattedTxtURL     string          `json:"formattedTxtUrl,omitempty"`
	Provider            ProviderInfo    `json:"provider"`
	Pages               []*PageResponse `json:"pages"`
	FormattingOptimized bool            `json:"formattingOptimized"`
	FormattedByAI       bool            `json:"formattedByAI"`
	FormattingInProgress bool           `json:"formattingInProgress"`
	FormattingTotalChunks int           `json:"formattingTotalChunks"`
	FormattingCompletedChunks int       `json:"formattingCompletedChunks"`
}

// TaskSummary is a lightweight representation used for listings.
type TaskSummary struct {
	ID             string    `json:"id"`
	FileName       string    `json:"fileName"`
	TotalPages     int       `json:"totalPages"`
	CompletedPages int       `json:"completedPages"`
	PendingPages   int       `json:"pendingPages"`
	ErrorPages     int       `json:"errorPages"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
