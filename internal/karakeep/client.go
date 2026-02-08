package karakeep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	baseURL *url.URL
	apiKey  string
	http    *http.Client

	// apiPrefix is path prefix for Karakeep API (e.g. /api/v1).
	// We auto-detect between common prefixes on the first requests.
	apiPrefix string
}

type ClientOpts struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration

	// Optional. If empty, defaults to auto-detect (prefers /api/v1).
	APIPrefix string
}

func NewClient(opts ClientOpts) (*Client, error) {
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		return nil, errors.New("base url is empty")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base url: %q", base)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("base url must be https: %q", base)
	}

	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		return nil, errors.New("api key is empty")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Normalize base URL to have no path; we'll join paths below.
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.Path = ""

	return &Client{
		baseURL: u,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
		},
		apiPrefix: pickPrefix(opts.APIPrefix),
	}, nil
}

type APIError struct {
	StatusCode int
	BodyPreview string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.BodyPreview) != "" {
		return fmt.Sprintf("karakeep api error: status=%d body_preview=%q", e.StatusCode, strings.TrimSpace(e.BodyPreview))
	}
	return fmt.Sprintf("karakeep api error: status=%d", e.StatusCode)
}

func (c *Client) CreateBookmark(ctx context.Context, urlStr string, title string, notes string) (Bookmark, int, error) {
	// NOTE: There are 2 Karakeep API shapes in the wild:
	// - docs.karakeep.app shows POST /bookmarks with {url, title?, notes?}
	// - some deployments (incl. yours) require a discriminated union with {type: "link"|"text"|"asset", ...}
	//
	// We implement the union form here, since the server returns ZodError expecting `type`.
	//
	// Link bookmark:
	//   { "type": "link", "url": "https://..." , "title"?: "..." }
	// Text note:
	//   { "type": "text", "text": "..." }
	//
	// For link extra text, we create link first then PATCH notes (best-effort).
	urlStr = strings.TrimSpace(urlStr)
	title = strings.TrimSpace(title)
	notes = strings.TrimSpace(notes)

	if urlStr != "" {
		body := map[string]any{
			"type": "link",
			"url":  urlStr,
		}
		if title != "" {
			body["title"] = title
		}

		var out Bookmark
		status, raw, err := c.doJSON(ctx, http.MethodPost, "/bookmarks", body, &out)
		if err != nil {
			return Bookmark{}, status, err
		}
		out.Raw = raw

		// Best-effort: attach notes after creation (field name may differ; try "notes" then "note").
		if out.ID != "" && notes != "" {
			_, _, _ = c.UpdateBookmark(ctx, out.ID, map[string]any{"notes": notes})
		}
		return out, status, nil
	}

	// Text note (no URL)
	body := map[string]any{
		"type": "text",
		"text": notes,
	}
	if title != "" {
		body["title"] = title
	}
	var out Bookmark
	status, raw, err := c.doJSON(ctx, http.MethodPost, "/bookmarks", body, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) GetBookmark(ctx context.Context, bookmarkID string) (Bookmark, int, error) {
	// Official doc page: GET /bookmarks/:bookmarkId
	// https://docs.karakeep.app/api/get-a-single-bookmark
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID)
	status, raw, err := c.doJSON(ctx, http.MethodGet, p, nil, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) UpdateBookmark(ctx context.Context, bookmarkID string, patch map[string]any) (Bookmark, int, error) {
	// Official doc page: PATCH /bookmarks/:bookmarkId
	// https://docs.karakeep.app/api/update-a-bookmark
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID)
	status, raw, err := c.doJSON(ctx, http.MethodPatch, p, patch, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) Summarize(ctx context.Context, bookmarkID string) (Bookmark, int, error) {
	// Official doc page: POST /bookmarks/:bookmarkId/summarize
	// https://docs.karakeep.app/api/summarize-a-bookmark
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID) + "/summarize"
	status, raw, err := c.doJSON(ctx, http.MethodPost, p, map[string]any{}, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) UploadAsset(ctx context.Context, data []byte, filename string, mime string) (Asset, int, error) {
	// Official doc page: POST /assets
	// https://docs.karakeep.app/api/upload-a-new-asset
	if strings.TrimSpace(filename) == "" {
		filename = "upload.bin"
	}
	if strings.TrimSpace(mime) == "" {
		mime = "application/octet-stream"
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Best-effort field name; docs should confirm. 'file' is the most common.
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return Asset{}, 0, err
	}
	if _, err := fw.Write(data); err != nil {
		return Asset{}, 0, err
	}
	_ = mw.WriteField("mime", mime)

	if err := mw.Close(); err != nil {
		return Asset{}, 0, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/assets", &buf)
	if err != nil {
		return Asset{}, 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	status, raw, err := c.do(req)
	if err != nil {
		return Asset{}, status, err
	}

	// Parse JSON into Asset best-effort.
	var out Asset
	if err := json.Unmarshal(raw, &out); err == nil {
		out.Raw = raw
		return out, status, nil
	}

	// Some APIs wrap data; try {data:{...}}
	var wrapper struct {
		Data Asset `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Data.ID != "" {
		wrapper.Data.Raw = raw
		return wrapper.Data, status, nil
	}

	return Asset{Raw: raw}, status, nil
}

func (c *Client) AttachAsset(ctx context.Context, bookmarkID string, assetID string) (Bookmark, int, error) {
	// Official doc page: POST /bookmarks/:bookmarkId/assets (name inferred from docs page)
	// https://docs.karakeep.app/api/attach-asset
	body := map[string]any{
		"assetId": assetID,
	}
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID) + "/assets"
	status, raw, err := c.doJSON(ctx, http.MethodPost, p, body, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) ReplaceAsset(ctx context.Context, bookmarkID string, assetID string) (Bookmark, int, error) {
	// https://docs.karakeep.app/api/replace-asset
	body := map[string]any{
		"assetId": assetID,
	}
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID) + "/assets"
	status, raw, err := c.doJSON(ctx, http.MethodPut, p, body, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) DetachAsset(ctx context.Context, bookmarkID string) (Bookmark, int, error) {
	// https://docs.karakeep.app/api/detach-asset
	var out Bookmark
	p := "/bookmarks/" + url.PathEscape(bookmarkID) + "/assets"
	status, raw, err := c.doJSON(ctx, http.MethodDelete, p, nil, &out)
	if err != nil {
		return Bookmark{}, status, err
	}
	out.Raw = raw
	return out, status, nil
}

func (c *Client) doJSON(ctx context.Context, method string, p string, body any, out any) (status int, raw json.RawMessage, err error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, p, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	status, raw, err = c.do(req)
	if err != nil {
		return status, raw, err
	}
	if out != nil && len(raw) > 0 {
		// best-effort: some APIs wrap with {data:...}
		if err := json.Unmarshal(raw, out); err == nil {
			return status, raw, nil
		}
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Data) > 0 {
			_ = json.Unmarshal(wrapper.Data, out)
		}
	}
	return status, raw, nil
}

func (c *Client) newRequest(ctx context.Context, method string, p string, body io.Reader) (*http.Request, error) {
	u := *c.baseURL
	// path.Join cleans slashes; ensure p is treated as relative.
	p = strings.TrimPrefix(p, "/")
	u.Path = path.Join(u.Path, strings.TrimPrefix(c.apiPrefix, "/"), p)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request) (int, json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10)) // cap bodies to avoid Telegram MESSAGE_TOO_LONG
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := strings.TrimSpace(string(b))
		if len(preview) > 600 {
			preview = preview[:600] + "…"
		}

		// Auto-detect API prefix on 404s: try switching between /api/v1 and /api.
		if resp.StatusCode == http.StatusNotFound {
			if alt, ok := c.altPrefix(); ok {
				// Retry once with alternate prefix.
				c.apiPrefix = alt
				req2 := req.Clone(req.Context())
				// rebuild URL
				u := *c.baseURL
				p := strings.TrimPrefix(req.URL.Path, "/")
				// Remove previous prefix segment if present, then re-join with new prefix.
				// Best-effort: just join new prefix with last 2 segments (bookmarks/...) if we can.
				parts := strings.Split(p, "/")
				// Keep tail after possible 'api' or 'api/v1'
				start := 0
				if len(parts) >= 1 && parts[0] == "api" {
					start = 1
					if len(parts) >= 2 && parts[1] == "v1" {
						start = 2
					}
				}
				tail := strings.Join(parts[start:], "/")
				u.Path = path.Join(strings.TrimPrefix(c.apiPrefix, "/"), tail)
				req2.URL, _ = url.Parse(u.String())

				resp2, err2 := c.http.Do(req2)
				if err2 == nil {
					defer resp2.Body.Close()
					b2, _ := io.ReadAll(io.LimitReader(resp2.Body, 32<<10))
					if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
						return resp2.StatusCode, b2, nil
					}
					preview2 := strings.TrimSpace(string(b2))
					if len(preview2) > 600 {
						preview2 = preview2[:600] + "…"
					}
					return resp2.StatusCode, b2, &APIError{StatusCode: resp2.StatusCode, BodyPreview: preview2}
				}
			}
		}

		return resp.StatusCode, b, &APIError{StatusCode: resp.StatusCode, BodyPreview: preview}
	}
	return resp.StatusCode, b, nil
}

func pickPrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/api/v1"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func (c *Client) altPrefix() (string, bool) {
	switch c.apiPrefix {
	case "/api/v1":
		return "/api", true
	case "/api":
		return "/api/v1", true
	default:
		return "/api/v1", true
	}
}

