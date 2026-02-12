package assets

import _ "embed"

// DefaultChineseFont holds an embedded CJK font for PDF export.
//
//go:embed fonts/LXGWWenKaiLite-Regular.ttf
var defaultChineseFont []byte

// DefaultChineseFont returns the embedded font bytes.
func DefaultChineseFont() []byte {
	return defaultChineseFont
}
