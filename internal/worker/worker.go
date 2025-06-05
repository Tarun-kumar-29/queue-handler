package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/models"
	"queue-handler-2/internal/queue"
	"queue-handler-2/internal/utils"

	"github.com/hibiken/asynq"
)

// Worker processes generic HTTP request tasks using Asynq
type Worker struct {
	queueManager *queue.Manager
	httpClient   *utils.HTTPClient
	config       *config.Config
}

// NewWorker creates a new worker
func NewWorker(queueManager *queue.Manager, config *config.Config) *Worker {
	return &Worker{
		queueManager: queueManager,
		httpClient:   utils.NewHTTPClient(config),
		config:       config,
	}
}

// ProcessHTTPRequest processes an HTTP request task (Asynq handler)
func (w *Worker) ProcessHTTPRequest(ctx context.Context, t *asynq.Task) error {
	// Parse job from task payload
	job, err := models.CreateFromAsynqPayload(t.Payload())
	if err != nil {
		return fmt.Errorf("failed to parse job from payload: %v: %w", err, asynq.SkipRetry)
	}

	log.Printf("🔄 Worker processing HTTP request: JobID=%s, RequestID=%s, URL=%s",
		job.ID, job.RequestID, job.URL)

	// Mark job as processing
	job.MarkAsProcessing()
	if err := w.queueManager.UpdateJob(job); err != nil {
		log.Printf("⚠️ Failed to update job status: %v", err)
	}

	// Make HTTP request
	response, err := w.makeHTTPRequest(ctx, job)
	if err != nil {
		return w.handleRequestError(job, err)
	}

	// Handle successful response based on completion mode
	return w.handleSuccessfulResponseAndWait(ctx, job, response)
}

// makeHTTPRequest executes the actual HTTP request
func (w *Worker) makeHTTPRequest(ctx context.Context, job *models.Job) (*models.JobResponse, error) {
	start := time.Now()

	// Create HTTP request
	response, err := w.httpClient.MakeRequest(job.URL, job.Method, job.Headers, job.Body)
	if err != nil {
		log.Printf("❌ HTTP request connection failed: JobID=%s, Error=%v", job.ID, err)
		return nil, err
	}

	duration := time.Since(start)

	httpResponse := &models.JobResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Headers,
		Body:       response.Body,
		Duration:   duration,
		Timestamp:  time.Now(),
	}

	// ⭐ FIXED: Check for HTTP error status codes and return error
	if w.shouldTreatStatusAsError(job, response.StatusCode) {
		log.Printf("❌ HTTP request returned error status: JobID=%s, Status=%d, Duration=%v",
			job.ID, response.StatusCode, duration)
		// Return error to trigger failure handling
		return httpResponse, fmt.Errorf("HTTP %d error: %s", response.StatusCode, response.Body)
	}

	log.Printf("✅ HTTP request completed: JobID=%s, Status=%d, Duration=%v",
		job.ID, response.StatusCode, duration)

	return httpResponse, nil
}

// shouldTreatStatusAsError determines if HTTP status code should be treated as error
func (w *Worker) shouldTreatStatusAsError(job *models.Job, statusCode int) bool {
	// Check job metadata for custom error handling
	if job.Metadata != nil {
		// Option 1: Custom success codes (treat everything else as error)
		if successCodes, ok := job.Metadata["success_status_codes"].([]interface{}); ok {
			for _, code := range successCodes {
				if codeInt, ok := code.(float64); ok && int(codeInt) == statusCode {
					return false // This status code is explicitly marked as success
				}
			}
			return true // Not in success list, treat as error
		}

		// Option 2: Custom error codes (treat these specific codes as errors)
		if errorCodes, ok := job.Metadata["error_status_codes"].([]interface{}); ok {
			for _, code := range errorCodes {
				if codeInt, ok := code.(float64); ok && int(codeInt) == statusCode {
					return true // This status code is explicitly marked as error
				}
			}
			return false // Not in error list, treat as success
		}

		// Option 3: Disable status code error checking
		if disableStatusCheck, ok := job.Metadata["ignore_http_errors"].(bool); ok && disableStatusCheck {
			return false // Don't treat any status codes as errors
		}
	}

	// Default behavior: treat 4xx and 5xx as errors
	return statusCode >= 400
}

// ⭐ FIXED: handleRequestError now returns error to mark as failed in Asynq dashboard
func (w *Worker) handleRequestError(job *models.Job, err error) error {
	log.Printf("❌ HTTP request failed: JobID=%s, Error=%v", job.ID, err)

	if job.CanRetry() {
		// Mark for retry and let Asynq handle it
		job.MarkForRetry(err.Error(), w.config.BackoffDelay)
		w.queueManager.UpdateJob(job)

		// Return error to trigger Asynq retry
		return fmt.Errorf("HTTP request failed (attempt %d/%d): %w",
			job.Retries, job.MaxRetries, err)
	}

	// Mark as permanently failed in our system
	job.MarkAsFailed(err.Error())
	w.queueManager.CompleteJob(job)

	// Send failure notification if webhook URL is configured
	if job.WebhookURL != "" {
		go w.sendFailureNotification(job, err.Error())
	}

	log.Printf("❌ Job permanently failed: JobID=%s, RequestID=%s", job.ID, job.RequestID)

	// ⭐ CRITICAL FIX: Return error to mark as failed in Asynq dashboard
	return fmt.Errorf("job permanently failed: %w", err)
}

// handleSuccessfulResponseAndWait handles successful HTTP responses based on completion mode
func (w *Worker) handleSuccessfulResponseAndWait(ctx context.Context, job *models.Job, response *models.JobResponse) error {
	// Check completion mode
	switch job.CompletionMode {
	case "immediate":
		return w.handleImmediateCompletion(job, response)
	case "webhook":
		return w.handleWebhookCompletion(ctx, job, response)
	default:
		// Default behavior based on webhook URL
		if job.WebhookURL == "" {
			return w.handleImmediateCompletion(job, response)
		} else {
			return w.handleWebhookCompletion(ctx, job, response)
		}
	}
}

// handleImmediateCompletion completes the job immediately after HTTP response
func (w *Worker) handleImmediateCompletion(job *models.Job, response *models.JobResponse) error {
	// Complete job immediately after HTTP response
	job.MarkAsCompleted(response)
	if err := w.queueManager.CompleteJob(job); err != nil {
		return fmt.Errorf("failed to complete job: %w", err)
	}

	log.Printf("✅ Job completed immediately: JobID=%s, RequestID=%s, Status=%d",
		job.ID, job.RequestID, response.StatusCode)
	return nil
}

// handleWebhookCompletion handles webhook-based completion with configurable timeout
func (w *Worker) handleWebhookCompletion(ctx context.Context, job *models.Job, response *models.JobResponse) error {
	if job.WebhookURL == "" {
		// No webhook URL provided, fall back to immediate completion
		log.Printf("⚠️ Webhook mode requested but no webhook URL provided, completing immediately")
		return w.handleImmediateCompletion(job, response)
	}

	// Extract external request ID from server response
	externalRequestID, err := w.extractRequestIDFromResponse(job, response.Body)
	if err != nil {
		log.Printf("⚠️ Failed to extract request_id from response: %v", err)
		externalRequestID = job.RequestID // Use the original job RequestID as fallback
	}

	// Store the mapping: external_request_id -> job_id
	if externalRequestID != job.RequestID {
		if err := w.queueManager.RedisClient.CreateCallIDMapping(externalRequestID, job.ID); err != nil {
			log.Printf("⚠️ Failed to create request_id mapping: %v", err)
		} else {
			log.Printf("🔗 Mapped external requestId %s to jobId %s", externalRequestID, job.ID)
		}
	}

	// Mark as awaiting webhook confirmation from external service
	job.MarkAsAwaitingWebhook(response)
	if err := w.queueManager.UpdateJob(job); err != nil {
		log.Printf("⚠️ Failed to update job status: %v", err)
	}

	log.Printf("🔗 Job awaiting webhook confirmation: JobID=%s, OriginalRequestID=%s, ExternalRequestID=%s",
		job.ID, job.RequestID, externalRequestID)

	// BLOCK HERE: Wait for webhook confirmation with configurable timeout
	return w.waitForWebhookConfirmation(ctx, job, externalRequestID)
}

// ⭐ FIXED: waitForWebhookConfirmation now returns appropriate errors for Asynq
func (w *Worker) waitForWebhookConfirmation(ctx context.Context, job *models.Job, externalRequestID string) error {
	// Create a channel to receive webhook notification
	webhookChan := make(chan bool, 1)
	done := make(chan struct{})

	// Determine timeout duration - with priority order:
	// 1. Job-specific timeout from metadata
	// 2. Config webhook timeout
	// 3. Default 10 minutes
	timeoutDuration := w.getWebhookTimeout(job)

	// Start monitoring for webhook in a goroutine
	go w.monitorWebhookStatus(job.ID, externalRequestID, webhookChan, done)

	log.Printf("⏳ Worker blocking, waiting for webhook: JobID=%s, ExternalRequestID=%s, Timeout=%v",
		job.ID, externalRequestID, timeoutDuration)

	// Block until webhook arrives or timeout
	select {
	case webhookReceived := <-webhookChan:
		close(done) // Signal the monitoring goroutine to stop
		if webhookReceived {
			// Webhook received successfully
			log.Printf("✅ Webhook confirmed, completing job: JobID=%s", job.ID)
			job.MarkAsCompleted(nil) // Response was stored earlier
			w.queueManager.CompleteJob(job)
			return nil // This will show as succeeded in Asynq
		} else {
			// Webhook failed or error occurred
			log.Printf("❌ Webhook confirmation failed: JobID=%s", job.ID)
			job.MarkAsFailed("webhook confirmation failed")
			w.queueManager.CompleteJob(job)
			// ⭐ Return error to mark as failed in Asynq dashboard
			return fmt.Errorf("webhook confirmation failed for job %s", job.ID)
		}

	case <-time.After(timeoutDuration):
		close(done) // Signal the monitoring goroutine to stop
		// Timeout waiting for webhook
		log.Printf("⏰ Webhook timeout after %v: JobID=%s, moving worker to next job",
			timeoutDuration, job.ID)

		// Handle timeout based on job configuration
		return w.handleWebhookTimeout(job, timeoutDuration)

	case <-ctx.Done():
		close(done) // Signal the monitoring goroutine to stop
		// Context cancelled (server shutdown, etc.)
		log.Printf("🛑 Context cancelled while waiting for webhook: JobID=%s", job.ID)
		job.MarkAsFailed("context cancelled")
		w.queueManager.CompleteJob(job)
		// ⭐ Return error to mark as failed in Asynq dashboard
		return fmt.Errorf("context cancelled while waiting for webhook: %w", ctx.Err())
	}
}

// getWebhookTimeout determines the webhook timeout duration with priority order
func (w *Worker) getWebhookTimeout(job *models.Job) time.Duration {
	// Priority 1: Job-specific timeout from metadata (in seconds)
	if job.Metadata != nil {
		if timeoutSeconds, ok := job.Metadata["webhook_timeout_seconds"].(float64); ok && timeoutSeconds > 0 {
			duration := time.Duration(timeoutSeconds) * time.Second
			log.Printf("🕐 Using job-specific webhook timeout: %v", duration)
			return duration
		}

		// Also check for minutes
		if timeoutMinutes, ok := job.Metadata["webhook_timeout_minutes"].(float64); ok && timeoutMinutes > 0 {
			duration := time.Duration(timeoutMinutes) * time.Minute
			log.Printf("🕐 Using job-specific webhook timeout: %v", duration)
			return duration
		}

		// Check for string format (e.g., "10m", "600s")
		if timeoutStr, ok := job.Metadata["webhook_timeout"].(string); ok && timeoutStr != "" {
			if duration, err := time.ParseDuration(timeoutStr); err == nil && duration > 0 {
				log.Printf("🕐 Using job-specific webhook timeout: %v", duration)
				return duration
			}
		}
	}

	// Priority 2: Config webhook timeout
	if w.config.WebhookTimeout > 0 {
		log.Printf("🕐 Using config webhook timeout: %v", w.config.WebhookTimeout)
		return w.config.WebhookTimeout
	}

	// Priority 3: Default 10 minutes
	defaultTimeout := 10 * time.Minute
	log.Printf("🕐 Using default webhook timeout: %v", defaultTimeout)
	return defaultTimeout
}

// ⭐ FIXED: handleWebhookTimeout now returns appropriate errors for Asynq
func (w *Worker) handleWebhookTimeout(job *models.Job, timeoutDuration time.Duration) error {
	// Check timeout strategy from job metadata
	timeoutStrategy := "fail" // Default strategy

	if job.Metadata != nil {
		if strategy, ok := job.Metadata["webhook_timeout_strategy"].(string); ok {
			timeoutStrategy = strings.ToLower(strategy)
		}
	}

	switch timeoutStrategy {
	case "retry":
		// Retry the job if retries are available
		if job.CanRetry() {
			errorMsg := fmt.Sprintf("webhook timeout after %v - retrying", timeoutDuration)
			job.MarkForRetry(errorMsg, w.config.BackoffDelay)
			w.queueManager.UpdateJob(job)

			log.Printf("🔄 Webhook timeout - job marked for retry: JobID=%s, Attempt=%d/%d",
				job.ID, job.Retries, job.MaxRetries)
			// Return error to trigger Asynq retry
			return fmt.Errorf("webhook timeout - retrying: %s", errorMsg)
		} else {
			// No more retries, fall through to fail
			timeoutStrategy = "fail"
		}
	case "complete":
		// Mark as completed despite timeout
		job.MarkAsCompleted(nil)
		w.queueManager.CompleteJob(job)

		log.Printf("✅ Webhook timeout - job marked as completed: JobID=%s", job.ID)
		return nil // This will show as succeeded in Asynq
	case "fail":
		// Mark as failed due to timeout
		errorMsg := fmt.Sprintf("webhook timeout after %v", timeoutDuration)
		job.MarkAsFailed(errorMsg)
		w.queueManager.CompleteJob(job)

		log.Printf("❌ Webhook timeout - job marked as failed: JobID=%s", job.ID)
		// ⭐ Return error to mark as failed in Asynq dashboard
		return fmt.Errorf("webhook timeout: %s", errorMsg)
	default:
		// Unknown strategy, default to fail
		errorMsg := fmt.Sprintf("webhook timeout after %v (unknown strategy: %s)", timeoutDuration, timeoutStrategy)
		job.MarkAsFailed(errorMsg)
		w.queueManager.CompleteJob(job)

		log.Printf("❌ Webhook timeout - job marked as failed (unknown strategy): JobID=%s", job.ID)
		// ⭐ Return error to mark as failed in Asynq dashboard
		return fmt.Errorf("webhook timeout with unknown strategy: %s", errorMsg)
	}

	return nil
}

// monitorWebhookStatus monitors Redis for webhook status updates
func (w *Worker) monitorWebhookStatus(jobID, externalRequestID string, webhookChan chan bool, done chan struct{}) {
	ticker := time.NewTicker(1 * time.Second) // Check every second
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// Monitoring should stop
			return
		case <-ticker.C:
			// Check if webhook has been received
			status, err := w.queueManager.RedisClient.GetWebhookStatus(jobID, externalRequestID)
			if err != nil {
				log.Printf("⚠️ Error checking webhook status: %v", err)
				continue
			}

			switch status {
			case "completed":
				log.Printf("✅ Webhook status: completed for JobID=%s", jobID)
				select {
				case webhookChan <- true:
				case <-done:
					// Main goroutine has already finished
				}
				return
			case "failed":
				log.Printf("❌ Webhook status: failed for JobID=%s", jobID)
				select {
				case webhookChan <- false:
				case <-done:
					// Main goroutine has already finished
				}
				return
			case "pending":
				// Still waiting, continue monitoring
				continue
			default:
				// Unknown status, continue monitoring
				continue
			}
		}
	}
}

// sendFailureNotification sends a failure notification to webhook URL
func (w *Worker) sendFailureNotification(job *models.Job, errorMsg string) {
	if err := w.httpClient.SendFailureWebhook(job.WebhookURL, job, errorMsg); err != nil {
		log.Printf("⚠️ Failed to send failure notification: JobID=%s, Error=%v", job.ID, err)
	} else {
		log.Printf("🔗 Failure notification sent: JobID=%s, RequestID=%s", job.ID, job.RequestID)
	}
}

// extractRequestIDFromResponse extracts the request_id from external server response
// This is now generalized to support multiple field names and configurable extraction
func (w *Worker) extractRequestIDFromResponse(job *models.Job, responseBody string) (string, error) {
	var responseData map[string]interface{}

	if err := json.Unmarshal([]byte(responseBody), &responseData); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}

	// Define possible field names for request ID extraction (in order of preference)
	possibleFields := []string{
		"request_id",
		"requestId",
		"call_id", // Backward compatibility
		"callId",  // Backward compatibility
		"id",
		"job_id",
		"jobId",
		"transaction_id",
		"transactionId",
		"reference_id",
		"referenceId",
		"external_id",
		"externalId",
	}

	// Check if job metadata specifies a custom extraction field
	if job.Metadata != nil {
		if customField, ok := job.Metadata["response_id_field"].(string); ok && customField != "" {
			// Try custom field first
			if requestID, ok := responseData[customField].(string); ok && requestID != "" {
				log.Printf("🔍 Extracted request_id using custom field '%s': %s", customField, requestID)
				return requestID, nil
			}
		}

		// Check if metadata specifies multiple possible fields
		if customFields, ok := job.Metadata["response_id_fields"].([]interface{}); ok {
			for _, field := range customFields {
				if fieldStr, ok := field.(string); ok {
					if requestID, ok := responseData[fieldStr].(string); ok && requestID != "" {
						log.Printf("🔍 Extracted request_id using custom field '%s': %s", fieldStr, requestID)
						return requestID, nil
					}
				}
			}
		}
	}

	// Try standard field names
	for _, fieldName := range possibleFields {
		if requestID, ok := responseData[fieldName].(string); ok && requestID != "" {
			log.Printf("🔍 Extracted request_id using field '%s': %s", fieldName, requestID)
			return requestID, nil
		}
	}

	// Try nested extraction if specified in metadata
	if job.Metadata != nil {
		if nestedPath, ok := job.Metadata["response_id_path"].(string); ok && nestedPath != "" {
			if requestID := w.extractNestedValue(responseData, nestedPath); requestID != "" {
				log.Printf("🔍 Extracted request_id using nested path '%s': %s", nestedPath, requestID)
				return requestID, nil
			}
		}
	}

	// If no field found, check for common nested structures
	commonNestedPaths := []string{
		"data.id",
		"data.request_id",
		"data.call_id",
		"result.id",
		"result.request_id",
		"response.id",
		"response.request_id",
	}

	for _, path := range commonNestedPaths {
		if requestID := w.extractNestedValue(responseData, path); requestID != "" {
			log.Printf("🔍 Extracted request_id using nested path '%s': %s", path, requestID)
			return requestID, nil
		}
	}

	return "", fmt.Errorf("request_id not found in response using any known field names")
}

// extractNestedValue extracts a value from nested JSON using dot notation (e.g., "data.id")
func (w *Worker) extractNestedValue(data map[string]interface{}, path string) string {
	if path == "" {
		return ""
	}

	// Split path by dots
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part - extract the actual value
			if value, ok := current[part].(string); ok {
				return value
			}
			return ""
		} else {
			// Intermediate part - navigate deeper
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				return ""
			}
		}
	}

	return ""
}
