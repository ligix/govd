package networking

import (
	"io"
	"net/http"
)

type HTTPClientInterface interface {
	Do(req *http.Request) (*http.Response, error)
}

type HTTPClient struct {
	Client        HTTPClientInterface
	Headers       map[string]string
	Cookies       []*http.Cookie
	Proxy         string
	EdgeProxy     string
	DownloadProxy string
	DisableProxy  bool
}

type NewHTTPClientOptions struct {
	Headers       map[string]string
	Cookies       []*http.Cookie
	Proxy         string
	EdgeProxy     string
	DownloadProxy string
	Impersonate   bool
	DisableProxy  bool
}

type RequestParams struct {
	Body    io.Reader
	Headers map[string]string
	Cookies []*http.Cookie
	// SkipCookies prevents the client-scoped cookies from being attached to
	// the request. Useful for endpoints that must be requested anonymously.
	SkipCookies bool
}

type EdgeProxyResponse struct {
	URL        string            `json:"url"`
	StatusCode int               `json:"status_code"`
	Text       string            `json:"text"`
	Headers    map[string]string `json:"headers"`
	Cookies    []string          `json:"cookies"`
}
