package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type APIResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Client — простой HTTP-клиент для вызова API gruzilla-executor.
// Используется в CLI-командах run/executors.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient создаёт клиент с фиксированным базовым URL и timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Call выполняет POST-запрос к path с JSON-телом и декодирует
// стандартизованный APIResponse.
// Метод не трактует status=error как transport-ошибку — это ответственность
// вызывающего кода.
func (c *Client) Call(path string, body any) (APIResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return APIResponse{}, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return APIResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return APIResponse{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var out APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return APIResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}
