package libs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 封装了原生 http.Client
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient 创建并返回一个新的 HTTP 客户端实例
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL: baseURL,
	}
}

// GetHTTPClient 返回底层 http.Client，便于直接处理非 JSON 响应等场景
func (c *Client) GetHTTPClient() *http.Client {
	return c.httpClient
}

// RequestOptions 用于在 POST 等请求中自定义请求头和请求体
type RequestOptions struct {
	Headers map[string]string
	Body    any // 可以是结构体、map 等，会自动序列化为 JSON
}

// Get 发送 GET 请求，并将响应解析到泛型对象 T 中
func Get[T any](ctx context.Context, c *Client, urlPath string, headers map[string]string) (*T, error) {
	fullURL := c.baseURL + urlPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}

	// 注入请求头
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return doRequest[T](c.httpClient, req)
}

// Post 发送 POST 请求，支持自定义 options（请求头/请求体），并将响应解析到泛型对象 T 中
func Post[T any](ctx context.Context, c *Client, urlPath string, opts *RequestOptions) (*T, error) {
	fullURL := c.baseURL + urlPath

	var bodyReader io.Reader
	if opts != nil && opts.Body != nil {
		jsonBytes, err := json.Marshal(opts.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}

	// 设置默认 Content-Type，如果 options 里有则会被覆盖
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// 注入自定义请求头
	if opts != nil && opts.Headers != nil {
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
	}

	return doRequest[T](c.httpClient, req)
}

// 内部核心执行函数，负责发送请求和解析 JSON 响应
func doRequest[T any](client *http.Client, req *http.Request) (*T, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 如果状态码不属于 2xx，返回错误
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// 读取并解析响应体
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response JSON: %w", err)
	}

	return &result, nil
}

// PostForm 发送 application/x-www-form-urlencoded 请求
func PostForm[T any](ctx context.Context, c *Client, urlPath string, headers map[string]string, formData url.Values) (*T, error) {
	fullURL := c.baseURL + urlPath

	// 将 url.Values 编码为表单字符串（例如 "ReqMessageBody=..."）
	bodyReader := strings.NewReader(formData.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST form request: %w", err)
	}

	// 强制设置 Content-Type 为表单格式
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 注入自定义请求头
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return doRequest[T](c.httpClient, req)
}
