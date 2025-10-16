package beclient

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"
)

// Client abstracts the backend API used to fetch patient payloads.
type Client interface {
	GetPatient(ctx context.Context, id string, inHeaders http.Header) (status int, body []byte, headers http.Header, err error)
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
	if v := inHeaders.Get("Accept-Language"); v != "" {
		req.Header.Set("Accept-Language", v)
	}
	if v := inHeaders.Get("Authorization"); v != "" {
		req.Header.Set("Authorization", v)
	}
	if v := inHeaders.Get("Referer"); v != "" {
		req.Header.Set("Referer", v)
	}
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
