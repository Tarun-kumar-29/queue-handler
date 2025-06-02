package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/models"
	"queue-handler-2/internal/queue"
	"queue-handler-2/internal/utils"

	"github.com/hibiken/asynq"
)

// Worker processes HTTP request tasks using Asynq
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

	log.Printf("🔄 Worker processing HTTP request: JobID=%s, CallID=%s, URL=%s",
		job.ID, job.CallID, job.URL)

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

	log.Printf("✅ HTTP request completed: JobID=%s, Status=%d, Duration=%v",
		job.ID, response.StatusCode, duration)

	return httpResponse, nil
}

// handleRequestError handles HTTP request errors
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

	// Mark as permanently failed
	job.MarkAsFailed(err.Error())
	w.queueManager.CompleteJob(job)

	// Send failure notification if webhook URL is configured
	if job.WebhookURL != "" {
		go w.sendFailureNotification(job, err.Error())
	}

	log.Printf("❌ Job permanently failed: JobID=%s, CallID=%s", job.ID, job.CallID)
	return nil // Don't retry
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

	log.Printf("✅ Job completed immediately: JobID=%s, CallID=%s, Status=%d",
		job.ID, job.CallID, response.StatusCode)
	return nil
}

// handleWebhookCompletion handles webhook-based completion
func (w *Worker) handleWebhookCompletion(ctx context.Context, job *models.Job, response *models.JobResponse) error {
	if job.WebhookURL == "" {
		// No webhook URL provided, fall back to immediate completion
		log.Printf("⚠️ Webhook mode requested but no webhook URL provided, completing immediately")
		return w.handleImmediateCompletion(job, response)
	}

	// Extract call_id from mimic server response
	callID, err := w.extractCallIDFromResponse(response.Body)
	if err != nil {
		log.Printf("⚠️ Failed to extract call_id from response: %v", err)
		callID = job.CallID // Use the original job CallID as fallback
	}

	// Store the mapping: external_call_id -> job_id
	if callID != job.CallID {
		if err := w.queueManager.RedisClient.CreateCallIDMapping(callID, job.ID); err != nil {
			log.Printf("⚠️ Failed to create call_id mapping: %v", err)
		} else {
			log.Printf("🔗 Mapped external callId %s to jobId %s", callID, job.ID)
		}
	}

	// Mark as awaiting webhook confirmation from external service
	job.MarkAsAwaitingWebhook(response)
	if err := w.queueManager.UpdateJob(job); err != nil {
		log.Printf("⚠️ Failed to update job status: %v", err)
	}

	log.Printf("🔗 Job awaiting webhook confirmation: JobID=%s, OriginalCallID=%s, ExternalCallID=%s",
		job.ID, job.CallID, callID)

	// BLOCK HERE: Wait for webhook confirmation
	return w.waitForWebhookConfirmation(ctx, job, callID)
}

// waitForWebhookConfirmation blocks until webhook is received or timeout
func (w *Worker) waitForWebhookConfirmation(ctx context.Context, job *models.Job, externalCallID string) error {
	// Create a channel to receive webhook notification
	webhookChan := make(chan bool, 1)
	done := make(chan struct{})
	timeoutDuration := w.config.WebhookTimeout

	// Start monitoring for webhook in a goroutine
	go w.monitorWebhookStatus(job.ID, externalCallID, webhookChan, done)

	log.Printf("⏳ Worker blocking, waiting for webhook: JobID=%s, ExternalCallID=%s", job.ID, externalCallID)

	// Block until webhook arrives or timeout
	select {
	case webhookReceived := <-webhookChan:
		close(done) // Signal the monitoring goroutine to stop
		if webhookReceived {
			// Webhook received successfully
			log.Printf("✅ Webhook confirmed, completing job: JobID=%s", job.ID)
			job.MarkAsCompleted(nil) // Response was stored earlier
			w.queueManager.CompleteJob(job)
			return nil
		} else {
			// Webhook failed or error occurred
			log.Printf("❌ Webhook confirmation failed: JobID=%s", job.ID)
			job.MarkAsFailed("webhook confirmation failed")
			w.queueManager.CompleteJob(job)
			return nil
		}

	case <-time.After(timeoutDuration):
		close(done) // Signal the monitoring goroutine to stop
		// Timeout waiting for webhook
		log.Printf("⏰ Webhook timeout: JobID=%s, waited %v", job.ID, timeoutDuration)
		job.MarkAsFailed("webhook timeout")
		w.queueManager.CompleteJob(job)
		return nil

	case <-ctx.Done():
		close(done) // Signal the monitoring goroutine to stop
		// Context cancelled (server shutdown, etc.)
		log.Printf("🛑 Context cancelled while waiting for webhook: JobID=%s", job.ID)
		job.MarkAsFailed("context cancelled")
		w.queueManager.CompleteJob(job)
		return ctx.Err()
	}
}

// monitorWebhookStatus monitors Redis for webhook status updates
func (w *Worker) monitorWebhookStatus(jobID, externalCallID string, webhookChan chan bool, done chan struct{}) {
	ticker := time.NewTicker(1 * time.Second) // Check every second
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// Monitoring should stop
			return
		case <-ticker.C:
			// Check if webhook has been received
			status, err := w.queueManager.RedisClient.GetWebhookStatus(jobID, externalCallID)
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
		log.Printf("🔗 Failure notification sent: JobID=%s, CallID=%s", job.ID, job.CallID)
	}
}

// extractCallIDFromResponse extracts the call_id from mimic server response
func (w *Worker) extractCallIDFromResponse(responseBody string) (string, error) {
	var responseData map[string]interface{}

	if err := json.Unmarshal([]byte(responseBody), &responseData); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}

	// Try different possible field names for call_id
	if callID, ok := responseData["callId"].(string); ok && callID != "" {
		return callID, nil
	}
	if callID, ok := responseData["call_id"].(string); ok && callID != "" {
		return callID, nil
	}
	if callID, ok := responseData["id"].(string); ok && callID != "" {
		return callID, nil
	}

	return "", fmt.Errorf("call_id not found in response")
}
