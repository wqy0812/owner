package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type apiClient struct {
	baseURL string
	http    *http.Client
}

type apiError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	Body    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("API status=%d code=%s message=%s", e.Status, e.Code, e.Message)
}

func newAPIClient(baseURL string) (*apiClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &apiClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}, nil
}

func (c *apiClient) switchIdentity(ctx context.Context, userID string) error {
	var user struct {
		ID string `json:"id"`
	}
	if err := c.data(ctx, http.MethodPost, "/api/v1/session/switch", map[string]string{"userId": userID}, &user); err != nil {
		return err
	}
	if user.ID != userID {
		return fmt.Errorf("identity switch returned %q, want %q", user.ID, userID)
	}
	return nil
}

func (c *apiClient) data(ctx context.Context, method, path string, input, output any) error {
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	status, body, err := c.request(ctx, method, path, input)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode %s %s response: %w (body=%s)", method, path, err, truncate(body))
	}
	if status < 200 || status >= 300 {
		apiErr := &apiError{Status: status, Body: string(body)}
		if envelope.Error != nil {
			apiErr.Code, apiErr.Message = envelope.Error.Code, envelope.Error.Message
		}
		return apiErr
	}
	if output == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("%s %s returned no data", method, path)
	}
	return json.Unmarshal(envelope.Data, output)
}

func (c *apiClient) items(ctx context.Context, path string, output any) error {
	status, body, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return &apiError{Status: status, Body: string(body)}
	}
	var envelope struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Items, output)
}

func (c *apiClient) request(ctx context.Context, method, path string, input any) (int, []byte, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, contents, err
}

func truncate(value []byte) string {
	const limit = 1024
	if len(value) <= limit {
		return string(value)
	}
	return string(value[:limit]) + "…"
}
