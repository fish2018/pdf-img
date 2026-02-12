package translator

import (
	"context"
	"fmt"
)

type contextKey string

const pageNumberKey contextKey = "pdftool_translator_page_number"

// WithPageNumber stores the current PDF page index inside the context for logging.
func WithPageNumber(ctx context.Context, pageNumber int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if pageNumber <= 0 {
		return ctx
	}
	return context.WithValue(ctx, pageNumberKey, pageNumber)
}

func pageNumberFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(pageNumberKey).(int); ok {
		return v
	}
	return 0
}

func formatPagePrefix(pageNumber int) string {
	if pageNumber <= 0 {
		return ""
	}
	return fmt.Sprintf("[Page %d] ", pageNumber)
}
