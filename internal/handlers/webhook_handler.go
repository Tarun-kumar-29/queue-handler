package handlers

import (
	// "context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"queue-handler-2/internal/models"
	"queue-handler-2/internal/queue"
)

type WebhookHandler struct {
	queueManager *queue.Manager
}

// Flexible webhook request structure to handle different external service formats
type WebhookRequest struct {
	// Different possible call_id field names
	CallID   string `json:"call_id"`
	CallId   string `json:"callId"`     // Alternative naming
	ID       string `json:"id"`         // Some services use just "id"
	
	// Different possible job_id field names  
	JobID    string `json:"job_id"`
	JobId    string `json:"jobId"`      // Alternative naming
	
	// Status fields - with flexible acceptance
	Status   string `json:"status"`
	State    string `json:"state"`      // Some services use "state"
	Result   string `json:"result"`     // Some services use "result"
	
	// Additional fields
	Response map[string]interface{} `json:"response,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`     // Some services wrap in "data"
	Error    string                 `json:"error,omitempty"`
	Message  string                 `json:"message,omitempty"`
	RetryAt  *time.Time             `json:"retry_at,omitempty"`
	
	// Raw map to capture any other fields
	Raw map[string]interface{} `json:"-"`
}

type WebhookResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	JobID   string `json:"job_id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

func NewWebhookHandler(queueManager *queue.Manager) *WebhookHandler {
	return &WebhookHandler{
		queueManager: queueManager,
	}
}

// HandleWebhook processes incoming webhook notifications with flexible format support
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// First, parse as raw JSON to capture everything
	var rawData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&rawData); err != nil {
		log.Printf("❌ Invalid webhook JSON: %v", err)
		h.sendErrorResponse(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	// Convert to our flexible webhook request
	webhookReq := h.parseFlexibleWebhook(rawData)
	
	log.Printf("🔗 Raw webhook received: %+v", rawData)
	log.Printf("🔗 Parsed webhook: CallID=%s, JobID=%s, Status=%s", 
		webhookReq.CallID, webhookReq.JobID, webhookReq.Status)

	// Validate we have either call_id or job_id
	if webhookReq.CallID == "" && webhookReq.JobID == "" {
		log.Printf("❌ Webhook missing identifiers: %+v", rawData)
		h.sendErrorResponse(w, "Either call_id or job_id is required", http.StatusBadRequest)
		return
	}

	// Default status if not provided
	if webhookReq.Status == "" {
		webhookReq.Status = "completed"
		log.Printf("🔗 No status provided, defaulting to 'completed'")
	}

	// Get job (prefer JobID, fallback to CallID)
	var job *models.Job
	var err error
	
	if webhookReq.JobID != "" {
		job, err = h.queueManager.GetJob(webhookReq.JobID)
		if err != nil {
			log.Printf("❌ Job not found by JobID=%s, trying CallID=%s", webhookReq.JobID, webhookReq.CallID)
		}
	}
	
	if job == nil && webhookReq.CallID != "" {
		job, err = h.queueManager.GetJobByCallID(webhookReq.CallID)
	}

	if err != nil || job == nil {
		log.Printf("❌ Failed to find job: CallID=%s, JobID=%s, Error=%v", 
			webhookReq.CallID, webhookReq.JobID, err)
		h.sendErrorResponse(w, "Job not found", http.StatusNotFound)
		return
	}

	log.Printf("🔗 Webhook received: CallID=%s, JobID=%s, Status=%s", 
		webhookReq.CallID, job.ID, webhookReq.Status)

	// CRITICAL: Update Redis status BEFORE processing to unblock waiting worker
	h.updateWebhookStatusInRedis(job.ID, webhookReq.CallID, webhookReq.Status)

	// Process webhook based on status
	if err := h.processWebhookConfirmation(job, &webhookReq); err != nil {
		log.Printf("❌ Failed to process webhook: JobID=%s, Error=%v", job.ID, err)
		// Even if processing fails, keep the Redis status to unblock worker
		h.sendErrorResponse(w, "Failed to process webhook", http.StatusInternalServerError)
		return
	}

	// Send successful response
	response := WebhookResponse{
		Success: true,
		Message: "Webhook processed successfully",
		JobID:   job.ID,
		CallID:  webhookReq.CallID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("✅ Webhook processed: CallID=%s, JobID=%s, FinalStatus=%s", 
		webhookReq.CallID, job.ID, job.Status)
}

// updateWebhookStatusInRedis updates the webhook status in Redis to unblock waiting workers
func (h *WebhookHandler) updateWebhookStatusInRedis(jobID, callID, status string) {
	// Normalize status for Redis storage
	redisStatus := "completed" // Default
	
	normalizedStatus := strings.ToLower(strings.TrimSpace(status))
	switch normalizedStatus {
	case "success", "completed", "complete", "ok", "done", "finished", "successful":
		redisStatus = "completed"
	case "fail", "failed", "failure", "error", "cancelled", "canceled", "aborted":
		redisStatus = "failed"
	case "retry", "reschedule", "requeue", "pending":
		redisStatus = "retry"
	default:
		redisStatus = "completed" // Default to completed for unknown statuses
	}

	// Update Redis status
	key := fmt.Sprintf("webhook_status:%s", jobID)
	err := h.queueManager.RedisClient.Set(key, redisStatus, time.Hour)
	if err != nil {
		log.Printf("⚠️ Failed to update webhook status in Redis: JobID=%s, Error=%v", jobID, err)
	} else {
		log.Printf("✅ Webhook status updated in Redis: JobID=%s, Status=%s", jobID, redisStatus)
	}

	// Also update with callID key for backup lookup
	if callID != "" && callID != jobID {
		callKey := fmt.Sprintf("webhook_status:call:%s", callID)
		err := h.queueManager.RedisClient.Set(callKey, redisStatus, time.Hour)
		if err != nil {
			log.Printf("⚠️ Failed to update webhook status by callID in Redis: CallID=%s, Error=%v", callID, err)
		}
	}
}

// parseFlexibleWebhook converts raw webhook data to standardized format
func (h *WebhookHandler) parseFlexibleWebhook(rawData map[string]interface{}) WebhookRequest {
	req := WebhookRequest{Raw: rawData}

	// Extract call_id from various possible field names
	if callID := h.extractStringField(rawData, "call_id", "callId", "id"); callID != "" {
		req.CallID = callID
	}

	// Extract job_id from various possible field names
	if jobID := h.extractStringField(rawData, "job_id", "jobId"); jobID != "" {
		req.JobID = jobID
	}

	// Extract status from various possible field names and normalize it
	if status := h.extractStringField(rawData, "status", "state", "result"); status != "" {
		req.Status = h.normalizeStatus(status)
	}

	// Extract error/message
	if errorMsg := h.extractStringField(rawData, "error", "error_message", "message"); errorMsg != "" {
		req.Error = errorMsg
	}

	// Extract additional message
	if msg := h.extractStringField(rawData, "message", "description", "reason"); msg != "" {
		req.Message = msg
	}

	// Extract response data
	if response, ok := rawData["response"].(map[string]interface{}); ok {
		req.Response = response
	} else if data, ok := rawData["data"].(map[string]interface{}); ok {
		req.Data = data
	}

	return req
}

// extractStringField tries multiple field names and returns the first non-empty value
func (h *WebhookHandler) extractStringField(data map[string]interface{}, fieldNames ...string) string {
	for _, fieldName := range fieldNames {
		if value, ok := data[fieldName]; ok {
			if strValue, ok := value.(string); ok && strValue != "" {
				return strValue
			}
		}
	}
	return ""
}

// normalizeStatus converts various status formats to our standard format
func (h *WebhookHandler) normalizeStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	
	// Map various success statuses
	successStatuses := []string{"success", "completed", "complete", "ok", "done", "finished", "successful"}
	for _, s := range successStatuses {
		if status == s {
			return "completed"
		}
	}
	
	// Map various failure statuses
	failureStatuses := []string{"fail", "failed", "failure", "error", "cancelled", "canceled", "aborted"}
	for _, s := range failureStatuses {
		if status == s {
			return "failed"
		}
	}
	
	// Map various retry statuses
	retryStatuses := []string{"retry", "reschedule", "requeue", "pending"}
	for _, s := range retryStatuses {
		if status == s {
			return "retry"
		}
	}
	
	// Return original if no mapping found
	return status
}

// processWebhookConfirmation handles different webhook confirmation types
func (h *WebhookHandler) processWebhookConfirmation(job *models.Job, webhookReq *WebhookRequest) error {
	status := strings.ToLower(webhookReq.Status)

	switch status {
	case "success", "completed", "ok", "done":
		return h.handleSuccessConfirmation(job, webhookReq)
		
	case "retry", "reschedule", "requeue":
		return h.handleRetryConfirmation(job, webhookReq)
		
	case "fail", "failed", "error", "cancel", "cancelled":
		return h.handleFailureConfirmation(job, webhookReq)
		
	default:
		// For unknown statuses, default to success
		log.Printf("⚠️ Unknown webhook status '%s', defaulting to success", webhookReq.Status)
		return h.handleSuccessConfirmation(job, webhookReq)
	}
}

// handleSuccessConfirmation processes successful completion confirmations
func (h *WebhookHandler) handleSuccessConfirmation(job *models.Job, webhookReq *WebhookRequest) error {
	// Verify job is in correct state
	if job.Status != models.JobStatusAwaitingHook {
		log.Printf("⚠️ Job %s is not awaiting webhook (status: %s), but processing anyway", 
			job.ID, job.Status)
	}

	// Update response data if provided
	if webhookReq.Response != nil && job.Response != nil {
		// Merge additional response data
		if body, ok := webhookReq.Response["body"].(string); ok && body != "" {
			job.Response.Body = body
		}
		if statusCode, ok := webhookReq.Response["status_code"].(float64); ok {
			job.Response.StatusCode = int(statusCode)
		}
	}

	// Mark job as completed
	job.MarkAsCompleted(job.Response)
	
	// Complete job in queue manager (this will remove webhook tracking)
	if err := h.queueManager.CompleteJob(job); err != nil {
		return fmt.Errorf("failed to complete job: %w", err)
	}

	log.Printf("✅ Job completed: ID=%s, Status=%s", job.ID, webhookReq.Status)
	log.Printf("✅ Job completed via webhook confirmation: ID=%s, CallID=%s", job.ID, job.CallID)
	return nil
}

// handleRetryConfirmation processes retry requests
func (h *WebhookHandler) handleRetryConfirmation(job *models.Job, webhookReq *WebhookRequest) error {
	// Check if job can be retried
	if !job.IsRetryable() {
		// Mark as failed if no more retries
		errorMsg := fmt.Sprintf("webhook requested retry but max retries exceeded (%d/%d)", 
			job.Retries, job.MaxRetries)
		job.MarkAsFailed(errorMsg)
		return h.queueManager.CompleteJob(job)
	}

	// Determine retry time
	retryAt := time.Now().Add(1 * time.Minute) // Default 1 minute delay
	if webhookReq.RetryAt != nil {
		retryAt = *webhookReq.RetryAt
	}

	// Mark job for retry
	errorMsg := "retry requested via webhook"
	if webhookReq.Message != "" {
		errorMsg = webhookReq.Message
	}
	
	// Reset job state for retry
	job.Status = models.JobStatusPending
	job.ScheduledAt = retryAt
	job.UpdatedAt = time.Now()
	job.StartedAt = nil
	job.Response = nil
	job.Error = errorMsg

	// Re-enqueue the job with Asynq
	if err := h.queueManager.EnqueueJob(job); err != nil {
		return fmt.Errorf("failed to re-enqueue job for retry: %w", err)
	}

	log.Printf("🔄 Job scheduled for retry via webhook: ID=%s, RetryAt=%s, Attempt=%d/%d", 
		job.ID, retryAt.Format(time.RFC3339), job.Retries, job.MaxRetries)
	
	return nil
}

// handleFailureConfirmation processes failure confirmations
func (h *WebhookHandler) handleFailureConfirmation(job *models.Job, webhookReq *WebhookRequest) error {
	// Mark job as failed
	errorMsg := "marked as failed via webhook"
	if webhookReq.Error != "" {
		errorMsg = webhookReq.Error
	} else if webhookReq.Message != "" {
		errorMsg = webhookReq.Message
	}

	job.MarkAsFailed(errorMsg)
	
	// Complete job in queue manager
	if err := h.queueManager.CompleteJob(job); err != nil {
		return fmt.Errorf("failed to mark job as failed: %w", err)
	}

	log.Printf("❌ Job marked as failed via webhook: ID=%s, CallID=%s, Reason=%s", 
		job.ID, job.CallID, errorMsg)
	
	return nil
}

// GetWebhookPendingJobs returns jobs awaiting webhook confirmation
func (h *WebhookHandler) GetWebhookPendingJobs(w http.ResponseWriter, r *http.Request) {
	pendingJobs, err := h.queueManager.GetWebhookPendingJobs()
	if err != nil {
		h.sendErrorResponse(w, "Failed to get pending webhook jobs", http.StatusInternalServerError)
		return
	}

	// Get job details for each pending job
	jobs := make([]*models.Job, 0, len(pendingJobs))
	for _, jobID := range pendingJobs {
		if job, err := h.queueManager.GetJob(jobID); err == nil {
			jobs = append(jobs, job)
		}
	}

	response := map[string]interface{}{
		"success":      true,
		"pending_jobs": jobs,
		"count":        len(jobs),
		"timestamp":    time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Helper method to send error responses
func (h *WebhookHandler) sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	response := WebhookResponse{
		Success: false,
		Message: message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}