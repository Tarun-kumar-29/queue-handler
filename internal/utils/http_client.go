package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/models"
)

type HTTPClient struct {
	client *http.Client
	config *config.Config
}

type HTTPResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       string
}

func NewHTTPClient(config *config.Config) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: config.HTTPTimeout,
		},
		config: config,
	}
}

// MakeRequest makes an HTTP request
func (h *HTTPClient) MakeRequest(url, method string, headers map[string]string, body string) (*HTTPResponse, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Set default headers if not provided
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Queue-Handler-Asynq/1.0")
	}

	// Set content type for JSON bodies
	if req.Header.Get("Content-Type") == "" && body != "" {
		if h.isJSON(body) {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	// Make request
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Extract response headers
	respHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			respHeaders[key] = values[0] // Take first value
		}
	}

	return &HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       string(respBody),
	}, nil
}

// SendWebhook sends a webhook notification with job completion data
func (h *HTTPClient) SendWebhook(webhookURL string, job *models.Job, response *models.JobResponse) error {
	payload := &models.WebhookPayload{
		CallID:      job.CallID,
		JobID:       job.ID,
		Status:      models.JobStatusCompleted,
		Response:    response,
		CompletedAt: time.Now(),
		Attempts:    job.Retries,
	}

	return h.sendWebhookPayload(webhookURL, job, payload)
}

// SendFailureWebhook sends a webhook notification for failed jobs
func (h *HTTPClient) SendFailureWebhook(webhookURL string, job *models.Job, errorMsg string) error {
	payload := &models.WebhookPayload{
		CallID:      job.CallID,
		JobID:       job.ID,
		Status:      models.JobStatusFailed,
		Error:       errorMsg,
		CompletedAt: time.Now(),
		Attempts:    job.Retries,
	}

	return h.sendWebhookPayload(webhookURL, job, payload)
}

// sendWebhookPayload sends the actual webhook payload
func (h *HTTPClient) sendWebhookPayload(webhookURL string, job *models.Job, payload *models.WebhookPayload) error {
	// Serialize payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to serialize webhook payload: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Queue-Handler-Asynq-Webhook/1.0")
	req.Header.Set("X-Queue-Handler-Event", "job.completed")
	req.Header.Set("X-Job-ID", job.ID)
	req.Header.Set("X-Call-ID", job.CallID)
	req.Header.Set("X-Job-Status", string(payload.Status))

	// Make request with webhook timeout
	client := &http.Client{
		Timeout: h.config.WebhookTimeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check if webhook was successful (2xx status codes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read error response
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ValidateURL checks if a URL is valid and accessible
func (h *HTTPClient) ValidateURL(url string) error {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Use a shorter timeout for validation
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("URL not accessible: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// isJSON checks if a string is valid JSON
func (h *HTTPClient) isJSON(str string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(str), &js) == nil
}

// GetContentType determines content type based on body content
func GetContentType(body string) string {
	if body == "" {
		return "text/plain"
	}

	// Try to parse as JSON
	var js json.RawMessage
	if json.Unmarshal([]byte(body), &js) == nil {
		return "application/json"
	}

	// Check if it looks like form data
	if strings.Contains(body, "=") && strings.Contains(body, "&") {
		return "application/x-www-form-urlencoded"
	}

	// Default to text
	return "text/plain"
}

// SanitizeHeaders removes sensitive headers from logging
func SanitizeHeaders(headers map[string]string) map[string]string {
	sanitized := make(map[string]string)
	sensitiveHeaders := map[string]bool{
		"authorization": true,
		"cookie":        true,
		"x-api-key":     true,
		"x-auth-token":  true,
	}

	for key, value := range headers {
		lowerKey := strings.ToLower(key)
		if sensitiveHeaders[lowerKey] {
			sanitized[key] = "[REDACTED]"
		} else {
			sanitized[key] = value
		}
	}

	return sanitized
}