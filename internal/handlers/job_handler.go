package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/models"
	"queue-handler-2/internal/queue"
	"queue-handler-2/internal/worker"

	"github.com/gorilla/mux"
)

type JobHandler struct {
	queueManager  *queue.Manager
	workerManager *worker.Manager
	config        *config.Config
}

type ScheduleJobResponse struct {
	Success   bool   `json:"success"`
	JobID     string `json:"job_id"`
	RequestID string `json:"request_id"` // Generalized from CallID
	TaskID    string `json:"task_id,omitempty"`
	Message   string `json:"message"`

	// Backward compatibility
	CallID string `json:"call_id,omitempty"` // For backward compatibility
}

type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func NewJobHandler(queueManager *queue.Manager, workerManager *worker.Manager, config *config.Config) *JobHandler {
	return &JobHandler{
		queueManager:  queueManager,
		workerManager: workerManager,
		config:        config,
	}
}

// ScheduleJob handles job scheduling requests with optional dedicated worker concurrency
func (h *JobHandler) ScheduleJob(w http.ResponseWriter, r *http.Request) {
	var jobReq models.JobRequest

	// Parse request body
	if err := json.NewDecoder(r.Body).Decode(&jobReq); err != nil {
		h.sendErrorResponse(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if jobReq.URL == "" {
		h.sendErrorResponse(w, "URL is required", http.StatusBadRequest)
		return
	}

	if jobReq.Method == "" {
		jobReq.Method = "GET"
	}

	// Validate HTTP method
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "HEAD": true, "OPTIONS": true,
	}
	if !validMethods[strings.ToUpper(jobReq.Method)] {
		h.sendErrorResponse(w, "Invalid HTTP method", http.StatusBadRequest)
		return
	}

	jobReq.Method = strings.ToUpper(jobReq.Method)

	// Set defaults
	if jobReq.QueueName == "" {
		jobReq.QueueName = "default"
	}
	if jobReq.MaxRetries == 0 {
		jobReq.MaxRetries = h.config.MaxRetries
	}

	// Validate concurrency if provided
	if jobReq.RequestedConcurrency < 0 {
		h.sendErrorResponse(w, "Concurrency must be 0 or positive", http.StatusBadRequest)
		return
	}
	if jobReq.RequestedConcurrency > 100 {
		h.sendErrorResponse(w, "Concurrency cannot exceed 100", http.StatusBadRequest)
		return
	}
	if jobReq.CompletionMode != "" {
		validModes := map[string]bool{
			"immediate": true,
			"webhook":   true,
		}
		if !validModes[jobReq.CompletionMode] {
			h.sendErrorResponse(w, "Invalid completion_mode. Use 'immediate' or 'webhook'", http.StatusBadRequest)
			return
		}
	}

	// Create job with requested concurrency
	job := models.NewJob(&jobReq)

	// Enqueue job (this will automatically start dedicated workers if needed)
	if err := h.queueManager.EnqueueJob(job); err != nil {
		log.Printf("❌ Failed to enqueue job: %v", err)
		h.sendErrorResponse(w, "Failed to schedule job", http.StatusInternalServerError)
		return
	}

	// Send successful response with enhanced information
	concurrency := job.RequestedConcurrency
	if concurrency == 0 {
		concurrency = h.config.DefaultConcurrency
	}

	response := ScheduleJobResponse{
		Success:   true,
		JobID:     job.ID,
		RequestID: job.RequestID,
		TaskID:    job.TaskID,
		CallID:    job.RequestID, // Backward compatibility
		Message: fmt.Sprintf("Job scheduled in queue '%s' with %d workers (completion: %s)",
			job.QueueName, concurrency, job.CompletionMode),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)

	log.Printf("✅ Job scheduled with dedicated workers: ID=%s, RequestID=%s, URL=%s, Queue=%s, Workers=%d",
		job.ID, job.RequestID, job.URL, job.QueueName, concurrency)
}

// ScheduleBulkJobs handles bulk job scheduling requests with generalized validation
func (h *JobHandler) ScheduleBulkJobs(w http.ResponseWriter, r *http.Request) {
	var bulkReq models.BulkJobRequest

	// Parse request body
	if err := json.NewDecoder(r.Body).Decode(&bulkReq); err != nil {
		h.sendErrorResponse(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	// Validate bulk request
	if len(bulkReq.Jobs) == 0 {
		h.sendErrorResponse(w, "At least one job is required", http.StatusBadRequest)
		return
	}

	if len(bulkReq.Jobs) > 1000 {
		h.sendErrorResponse(w, "Maximum 100 jobs allowed per bulk request", http.StatusBadRequest)
		return
	}

	// Validate each job with generalized logic
	for i, job := range bulkReq.Jobs {
		// Core validation - URL is always required
		if job.URL == "" {
			h.sendErrorResponse(w, fmt.Sprintf("Job %d: URL is required", i+1), http.StatusBadRequest)
			return
		}

		// Flexible identification validation - check for any identification fields
		hasIdentification := false
		identificationFields := []string{"job_id", "request_id", "agent_id", "prospect_id", "call_id"}

		// Check direct fields
		if job.JobID != "" || job.RequestID != "" || job.AgentID != "" || job.ProspectID != "" || job.CallID != "" {
			hasIdentification = true
		}

		// Check metadata for identification
		if !hasIdentification && job.Metadata != nil {
			for _, field := range identificationFields {
				if val, exists := job.Metadata[field]; exists && val != nil {
					if strVal, ok := val.(string); ok && strVal != "" {
						hasIdentification = true
						break
					}
				}
			}

			// Check for common ID fields in metadata
			commonIDFields := []string{"user_id", "customer_id", "tenant_id", "service_id", "entity_id"}
			if !hasIdentification {
				for _, field := range commonIDFields {
					if val, exists := job.Metadata[field]; exists && val != nil {
						if strVal, ok := val.(string); ok && strVal != "" {
							hasIdentification = true
							break
						}
					}
				}
			}
		}

		// Backward compatibility: still require agent_id and prospect_id for call-related jobs
		// This can be removed once all clients are updated
		if job.AgentID == "" && job.ProspectID == "" && !hasIdentification {
			// Check if this is a legacy call-related job request
			isLegacyCall := (job.Metadata == nil ||
				(job.Metadata["job_type"] == nil && job.Metadata["service_name"] == nil))

			if isLegacyCall {
				h.sendErrorResponse(w, fmt.Sprintf("Job %d: identification required (agent_id, prospect_id, job_id, request_id, or metadata identification)", i+1), http.StatusBadRequest)
				return
			}
		}

		// Validate HTTP method if provided
		if job.Method != "" {
			validMethods := map[string]bool{
				"GET": true, "POST": true, "PUT": true, "DELETE": true,
				"PATCH": true, "HEAD": true, "OPTIONS": true,
			}
			method := strings.ToUpper(job.Method)
			if !validMethods[method] {
				h.sendErrorResponse(w, fmt.Sprintf("Job %d: Invalid HTTP method '%s'", i+1, job.Method), http.StatusBadRequest)
				return
			}
			bulkReq.Jobs[i].Method = method
		}

		// Validate concurrency if provided
		if job.RequestedConcurrency < 0 {
			h.sendErrorResponse(w, fmt.Sprintf("Job %d: Concurrency must be 0 or positive", i+1), http.StatusBadRequest)
			return
		}
		if job.RequestedConcurrency > 100 {
			h.sendErrorResponse(w, fmt.Sprintf("Job %d: Concurrency cannot exceed 100", i+1), http.StatusBadRequest)
			return
		}

		// Validate completion mode if provided
		if job.CompletionMode != "" {
			validModes := map[string]bool{
				"immediate": true,
				"webhook":   true,
			}
			if !validModes[job.CompletionMode] {
				h.sendErrorResponse(w, fmt.Sprintf("Job %d: Invalid completion_mode. Use 'immediate' or 'webhook'", i+1), http.StatusBadRequest)
				return
			}
		}
	}

	// Validate global defaults
	if bulkReq.DefaultMethod != "" {
		validMethods := map[string]bool{
			"GET": true, "POST": true, "PUT": true, "DELETE": true,
			"PATCH": true, "HEAD": true, "OPTIONS": true,
		}
		method := strings.ToUpper(bulkReq.DefaultMethod)
		if !validMethods[method] {
			h.sendErrorResponse(w, fmt.Sprintf("Invalid default HTTP method '%s'", bulkReq.DefaultMethod), http.StatusBadRequest)
			return
		}
		bulkReq.DefaultMethod = method
	}

	if bulkReq.DefaultConcurrency < 0 {
		h.sendErrorResponse(w, "Default concurrency must be 0 or positive", http.StatusBadRequest)
		return
	}

	if bulkReq.DefaultConcurrency > 1000 {
		h.sendErrorResponse(w, "Default concurrency cannot exceed 100", http.StatusBadRequest)
		return
	}

	// Set global defaults if not provided
	if bulkReq.DefaultMethod == "" {
		bulkReq.DefaultMethod = "POST"
	}
	if bulkReq.DefaultMaxRetries == 0 {
		bulkReq.DefaultMaxRetries = h.config.MaxRetries
	}
	if bulkReq.DefaultConcurrency == 0 {
		bulkReq.DefaultConcurrency = h.config.DefaultConcurrency
	}

	// Process bulk jobs
	response, err := h.queueManager.EnqueueBulkJobs(&bulkReq)
	if err != nil {
		log.Printf("❌ Failed to process bulk jobs: %v", err)
		h.sendErrorResponse(w, "Failed to process bulk job request", http.StatusInternalServerError)
		return
	}

	// Set appropriate HTTP status
	statusCode := http.StatusCreated
	if response.Failed > 0 {
		statusCode = http.StatusMultiStatus // 207 - Some succeeded, some failed
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)

	log.Printf("📦 Bulk job request completed: %d successful, %d failed, %d queues used",
		response.Successful, response.Failed, len(response.QueueSummary))
}

// GetJob returns information about a specific job
func (h *JobHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["id"]

	job, err := h.queueManager.GetJob(jobID)
	if err != nil {
		log.Printf("❌ Failed to get job %s: %v", jobID, err)
		h.sendErrorResponse(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(job)
}

// GetJobByRequestID returns information about a job by RequestID
func (h *JobHandler) GetJobByRequestID(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	requestID := vars["request_id"]

	job, err := h.queueManager.GetJobByRequestID(requestID)
	if err != nil {
		log.Printf("❌ Failed to get job by RequestID %s: %v", requestID, err)
		h.sendErrorResponse(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(job)
}

// GetJobByCallID returns information about a job by CallID (backward compatibility)
func (h *JobHandler) GetJobByCallID(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	callID := vars["call_id"]

	job, err := h.queueManager.GetJobByCallID(callID)
	if err != nil {
		log.Printf("❌ Failed to get job by CallID %s: %v", callID, err)
		h.sendErrorResponse(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(job)
}

// ClearQueue clears all jobs from a specific queue
func (h *JobHandler) ClearQueue(w http.ResponseWriter, r *http.Request) {
	var req models.ClearQueueRequest

	// Parse request body
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendErrorResponse(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	// Validate queue name
	if req.QueueName == "" {
		h.sendErrorResponse(w, "Queue name is required", http.StatusBadRequest)
		return
	}

	// Clear the queue
	deletedCount, err := h.queueManager.ClearQueue(req.QueueName)
	if err != nil {
		log.Printf("❌ Failed to clear queue %s: %v", req.QueueName, err)
		h.sendErrorResponse(w, fmt.Sprintf("Failed to clear queue: %v", err), http.StatusInternalServerError)
		return
	}

	// Send response
	response := models.ClearQueueResponse{
		Success:     true,
		QueueName:   req.QueueName,
		Message:     fmt.Sprintf("Successfully cleared queue '%s'", req.QueueName),
		DeletedJobs: deletedCount,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("🧹 Queue cleared via API: Queue=%s, DeletedJobs=%d", req.QueueName, deletedCount)
}

// GetQueueStats returns statistics for a specific queue using Asynq
func (h *JobHandler) GetQueueStats(w http.ResponseWriter, r *http.Request) {
	queueName := r.URL.Query().Get("queue")
	if queueName == "" {
		queueName = "default"
	}

	stats, err := h.queueManager.GetQueueStats(queueName)
	if err != nil {
		log.Printf("❌ Failed to get queue stats: %v", err)
		h.sendErrorResponse(w, "Failed to get queue statistics", http.StatusInternalServerError)
		return
	}

	// Add worker information
	stats.WorkersActive = h.workerManager.GetWorkerCount(queueName)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// Rest of the handler methods remain largely the same...
// [Including ListQueues, ScaleQueue, GetWorkerStats, etc. with updated logging]

// ListQueues returns all active queues
func (h *JobHandler) ListQueues(w http.ResponseWriter, r *http.Request) {
	queues, err := h.queueManager.ListQueues()
	if err != nil {
		h.sendErrorResponse(w, "Failed to list queues", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"queues":  queues,
		"count":   len(queues),
		"backend": "asynq",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// ScaleQueue adjusts the number of workers for a queue
func (h *JobHandler) ScaleQueue(w http.ResponseWriter, r *http.Request) {
	queueName := r.URL.Query().Get("queue")
	if queueName == "" {
		h.sendErrorResponse(w, "Queue name is required", http.StatusBadRequest)
		return
	}

	workersStr := r.URL.Query().Get("workers")
	if workersStr == "" {
		h.sendErrorResponse(w, "Number of workers is required", http.StatusBadRequest)
		return
	}

	workers, err := strconv.Atoi(workersStr)
	if err != nil || workers < 0 {
		h.sendErrorResponse(w, "Invalid number of workers", http.StatusBadRequest)
		return
	}

	// Scale workers
	if err := h.workerManager.ScaleWorkers(queueName, workers); err != nil {
		h.sendErrorResponse(w, "Failed to scale workers", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"queue":   queueName,
		"workers": workers,
		"message": fmt.Sprintf("Scaled queue '%s' to %d workers", queueName, workers),
		"backend": "asynq",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetWorkerStats returns worker statistics
func (h *JobHandler) GetWorkerStats(w http.ResponseWriter, r *http.Request) {
	stats := h.workerManager.GetWorkerStats()

	// Add active jobs information
	activeJobs, err := h.workerManager.GetActiveJobs()
	if err == nil {
		stats["active_jobs"] = activeJobs
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// GetActiveQueuesDetailed returns detailed information about all active queues
func (h *JobHandler) GetActiveQueuesDetailed(w http.ResponseWriter, r *http.Request) {
	activeQueues := h.queueManager.GetActiveQueues()

	response := map[string]interface{}{
		"success":       true,
		"active_queues": activeQueues,
		"total_queues":  len(activeQueues),
		"backend":       "asynq-dedicated",
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// ManualStartQueue manually starts workers for a queue with specified concurrency
func (h *JobHandler) ManualStartQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueName := vars["queue"]

	concurrencyStr := r.URL.Query().Get("concurrency")
	concurrency := h.config.DefaultConcurrency

	if concurrencyStr != "" {
		if c, err := strconv.Atoi(concurrencyStr); err == nil && c > 0 {
			concurrency = c
		}
	}

	if err := h.queueManager.RestartQueue(queueName, concurrency); err != nil {
		h.sendErrorResponse(w, fmt.Sprintf("Failed to start queue: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success":     true,
		"queue":       queueName,
		"concurrency": concurrency,
		"message":     fmt.Sprintf("Queue '%s' started with %d dedicated workers", queueName, concurrency),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("🚀 Manually started queue: %s with %d workers", queueName, concurrency)
}

// ManualStopQueue manually stops workers for a queue
func (h *JobHandler) ManualStopQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueName := vars["queue"]

	if err := h.queueManager.ForceShutdownQueue(queueName); err != nil {
		h.sendErrorResponse(w, fmt.Sprintf("Failed to stop queue: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"queue":   queueName,
		"message": fmt.Sprintf("Queue '%s' workers stopped", queueName),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("🛑 Manually stopped queue: %s", queueName)
}

// DeleteJob deletes a job from the queue
func (h *JobHandler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["id"]

	if err := h.queueManager.DeleteJob(jobID); err != nil {
		log.Printf("❌ Failed to delete job %s: %v", jobID, err)
		h.sendErrorResponse(w, "Failed to delete job", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"job_id":  jobID,
		"message": "Job deleted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("🗑️ Job deleted: ID=%s", jobID)
}

// ArchiveJob archives a job
func (h *JobHandler) ArchiveJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["id"]

	if err := h.queueManager.ArchiveJob(jobID); err != nil {
		log.Printf("❌ Failed to archive job %s: %v", jobID, err)
		h.sendErrorResponse(w, "Failed to archive job", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"job_id":  jobID,
		"message": "Job archived successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("📦 Job archived: ID=%s", jobID)
}

// PauseQueue pauses a queue
func (h *JobHandler) PauseQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueName := vars["queue"]

	if err := h.queueManager.PauseQueue(queueName); err != nil {
		log.Printf("❌ Failed to pause queue %s: %v", queueName, err)
		h.sendErrorResponse(w, "Failed to pause queue", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"queue":   queueName,
		"message": "Queue paused successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("⏸️ Queue paused: %s", queueName)
}

// UnpauseQueue resumes a queue
func (h *JobHandler) UnpauseQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueName := vars["queue"]

	if err := h.queueManager.UnpauseQueue(queueName); err != nil {
		log.Printf("❌ Failed to unpause queue %s: %v", queueName, err)
		h.sendErrorResponse(w, "Failed to unpause queue", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"queue":   queueName,
		"message": "Queue resumed successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("▶️ Queue resumed: %s", queueName)
}

// Helper method to send error responses
func (h *JobHandler) sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	response := ErrorResponse{
		Success: false,
		Error:   message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}
