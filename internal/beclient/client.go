package beclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client abstracts the backend API used to fetch patient payloads.
type Client interface {
	GetPatient(ctx context.Context, id string, inHeaders http.Header) (status int, body []byte, headers http.Header, err error)
	SearchPatients(ctx context.Context, q map[string][]string, inHeaders http.Header) (status int, body []byte, headers http.Header, err error)
}

// HTTPClient is a concrete Client using net/http.
type HTTPClient struct {
	BaseURL  string
	Timeout  time.Duration
	Insecure bool // mirrors curl -k for dev
}

func NewHTTPClient(baseURL string, timeout time.Duration, insecure bool) *HTTPClient {
	return &HTTPClient{BaseURL: baseURL, Timeout: timeout, Insecure: insecure}
}

func (c *HTTPClient) httpClient() *http.Client {
	tr := http.DefaultTransport
	if c.Insecure {
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return &http.Client{Timeout: c.Timeout, Transport: tr}
}

func (c *HTTPClient) GetPatient(ctx context.Context, id string, inHeaders http.Header) (int, []byte, http.Header, error) {
	urlStr := c.BaseURL + "/" + id + "?includeClosed=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	// Important headers from incoming request, with defaults if missing
	setOrDefault := func(name, def string) {
		if v := inHeaders.Get(name); v != "" {
			req.Header.Set(name, v)
		} else if def != "" {
			req.Header.Set(name, def)
		}
	}
	setOrDefault("Accept", "application/json, text/plain, */*")
	if v := inHeaders.Get("Accept-Language"); v != "" { req.Header.Set("Accept-Language", v) }
	if v := inHeaders.Get("Authorization"); v != "" { req.Header.Set("Authorization", v) }
	if v := inHeaders.Get("Referer"); v != "" { req.Header.Set("Referer", v) }
	setOrDefault("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	// Required X-* headers for BE
	setOrDefault("X-Group", "58")
	setOrDefault("X-Hospital", "59")
	setOrDefault("X-Location", "59")
	setOrDefault("X-Module", "empi")
	setOrDefault("X-User", "8008")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, resp.Header.Clone(), err
	}
	return resp.StatusCode, b, resp.Header.Clone(), nil
}

func (c *HTTPClient) SearchPatients(ctx context.Context, q map[string][]string, inHeaders http.Header) (int, []byte, http.Header, error) {
	// Build URL: {BaseURL}/pagination?lang=en&internationalization=true
	u, err := url.Parse(c.BaseURL + "/pagination")
	if err != nil {
		return 0, nil, nil, err
	}
	params := url.Values{}
	// Defaults from provided curl
	params.Set("lang", "en")
	params.Set("internationalization", "true")
	u.RawQuery = params.Encode()

	// Build POST body according to backend contract
	// Map FHIR-style query to backend metaParams.searchParams (JSON string)
	firstName := ""
	lastName := ""
	if vs, ok := q["firstName"]; ok && len(vs) > 0 { firstName = strings.TrimSpace(vs[0]) }
	if vs, ok := q["lastName"]; ok && len(vs) > 0 { lastName = strings.TrimSpace(vs[0]) }
	searchMap := map[string]any{}
	if firstName != "" { searchMap["firstName"] = firstName }
	if lastName != "" { searchMap["lastName"] = lastName }
	searchJSON, _ := json.Marshal(searchMap)

	// Page size: derive from _count if provided, else 10; startRow=0
	endRow := 10
	if vs, ok := q["_count"]; ok && len(vs) > 0 {
		if n, err := strconv.Atoi(vs[0]); err == nil && n > 0 { endRow = n }
	}
	payload := map[string]any{
		"startRow": 0,
		"endRow":   endRow,
		"searchParams": map[string]any{
			"startRow":     0,
			"endRow":       endRow,
			"rowGroupCols": []any{},
			"valueCols":    []any{},
			"pivotCols":    []any{},
			"pivotMode":    false,
			"groupKeys":    []any{},
			"filterModel":  map[string]any{},
			"sortModel":    []any{},
		},
		"metaParams": map[string]any{
			"searchParams":           string(searchJSON),
			"includeClosed":          false,
			"includeHoldMerged":      false,
			"includeChildProfiles":   false,
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, nil, err
	}
	setOrDefault := func(name, def string) {
		if v := inHeaders.Get(name); v != "" {
			req.Header.Set(name, v)
		} else if def != "" {
			req.Header.Set(name, def)
		}
	}
	// Headers per sample
	setOrDefault("Accept", "application/json, text/plain, */*")
	if v := inHeaders.Get("Accept-Language"); v != "" { req.Header.Set("Accept-Language", v) }
	if v := inHeaders.Get("Authorization"); v != "" { req.Header.Set("Authorization", v) }
	if v := inHeaders.Get("Origin"); v != "" { req.Header.Set("Origin", v) }
	if v := inHeaders.Get("Referer"); v != "" { req.Header.Set("Referer", v) }
	setOrDefault("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	setOrDefault("X-Group", "58")
	setOrDefault("X-Hospital", "59")
	setOrDefault("X-Location", "59")
	setOrDefault("X-Module", "empi")
	setOrDefault("X-User", "8008")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, resp.Header.Clone(), err
	}
	return resp.StatusCode, b, resp.Header.Clone(), nil
}
