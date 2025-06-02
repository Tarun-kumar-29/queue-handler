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
	Success bool   `json:"success"`
	JobID   string `json:"job_id"`
	CallID  string `json:"call_id"`
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message"`
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
		Success: true,
		JobID:   job.ID,
		CallID:  job.CallID,
		TaskID:  job.TaskID,
		Message: fmt.Sprintf("Job scheduled in queue '%s' with %d workers (completion: %s)",
			job.QueueName, concurrency, job.CompletionMode),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)

	log.Printf("✅ Job scheduled with dedicated workers: ID=%s, CallID=%s, URL=%s, Queue=%s, Workers=%d",
		job.ID, job.CallID, job.URL, job.QueueName, concurrency)
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

// GetJobByCallID returns information about a job by CallID
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

// ScaleQueue adjusts the number of workers for a queue (limited with Asynq)
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

	// Asynq doesn't support dynamic scaling, inform user
	if err := h.workerManager.ScaleWorkers(queueName, workers); err != nil {
		h.sendErrorResponse(w, "Failed to scale workers", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"queue":   queueName,
		"workers": workers,
		"message": fmt.Sprintf("Scaling request noted for queue '%s'. Asynq requires restart for scaling.", queueName),
		"backend": "asynq",
		"note":    "Dynamic scaling requires worker restart with new concurrency configuration",
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
