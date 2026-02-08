package karakeep

import (
	"encoding/json"
	"strings"
)

// Tag is a best-effort representation of Karakeep Tag.
type Tag struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Bookmark is a best-effort representation of Karakeep Bookmark.
// We keep Raw to stay resilient to schema changes and still be able to display something.
type Bookmark struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	Notes string `json:"notes,omitempty"`

	// Summary might be a string or an object; keep as raw.
	Summary json.RawMessage `json:"summary,omitempty"`

	Tags []Tag `json:"tags,omitempty"`

	Raw json.RawMessage `json:"-"`
}

func (b Bookmark) SummaryText() string {
	if len(b.Summary) == 0 {
		return ""
	}
	// Try string.
	var s string
	if err := json.Unmarshal(b.Summary, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Try object with common fields.
	var obj map[string]any
	if err := json.Unmarshal(b.Summary, &obj); err == nil {
		if v, ok := obj["text"].(string); ok {
			return strings.TrimSpace(v)
		}
		if v, ok := obj["summary"].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Asset is a best-effort representation of Karakeep Asset.
type Asset struct {
	ID       string `json:"id,omitempty"`
	Filename string `json:"filename,omitempty"`
	Mime     string `json:"mime,omitempty"`
	URL      string `json:"url,omitempty"`

	Raw json.RawMessage `json:"-"`
}

