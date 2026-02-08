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
}

type ClientOpts struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
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

	return &Client{
		baseURL: u,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body == "" {
		return fmt.Sprintf("karakeep api error: status=%d", e.StatusCode)
	}
	return fmt.Sprintf("karakeep api error: status=%d body=%s", e.StatusCode, e.Body)
}

func (c *Client) CreateBookmark(ctx context.Context, urlStr string, title string, notes string) (Bookmark, int, error) {
	// Official doc page: POST /bookmarks
	// https://docs.karakeep.app/api/create-a-new-bookmark
	body := map[string]any{}
	if strings.TrimSpace(urlStr) != "" {
		body["url"] = urlStr
	}
	if strings.TrimSpace(title) != "" {
		body["title"] = title
	}
	if strings.TrimSpace(notes) != "" {
		body["notes"] = notes
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
	u.Path = path.Join(u.Path, p)
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

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // cap error bodies
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, b, &APIError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	return resp.StatusCode, b, nil
}

