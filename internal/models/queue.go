package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStatus represents the status of a job
type JobStatus string

const (
	JobStatusPending      JobStatus = "pending"
	JobStatusProcessing   JobStatus = "processing"
	JobStatusAwaitingHook JobStatus = "awaiting_webhook"
	JobStatusCompleted    JobStatus = "completed"
	JobStatusFailed       JobStatus = "failed"
	JobStatusRetrying     JobStatus = "retrying"
)

// Job represents a job in the queue (compatible with Asynq)
type Job struct {
	ID                   string            `json:"id"`
	CallID               string            `json:"call_id"`
	URL                  string            `json:"url"`
	Method               string            `json:"method"`
	Headers              map[string]string `json:"headers,omitempty"`
	Body                 string            `json:"body,omitempty"`
	QueueName            string            `json:"queue_name"`
	Status               JobStatus         `json:"status"`
	Priority             int               `json:"priority"`
	Retries              int               `json:"retries"`
	MaxRetries           int               `json:"max_retries"`
	RequestedConcurrency int               `json:"requested_concurrency,omitempty"` // NEW: Requested workers for queue
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	ScheduledAt          time.Time         `json:"scheduled_at,omitempty"`
	StartedAt            *time.Time        `json:"started_at,omitempty"`
	CompletedAt          *time.Time        `json:"completed_at,omitempty"`
	Error                string            `json:"error,omitempty"`
	Response             *JobResponse      `json:"response,omitempty"`
	WebhookURL           string            `json:"webhook_url,omitempty"`
	CompletionMode       string            `json:"completion_mode"` // "immediate" or "webhook"

	// Asynq specific fields
	TaskID    string                 `json:"task_id,omitempty"`
	AsynqInfo map[string]interface{} `json:"asynq_info,omitempty"`
}

// JobResponse represents the response from processing a job
type JobResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Duration   time.Duration     `json:"duration"`
	Timestamp  time.Time         `json:"timestamp"`
}

// JobRequest represents a request to schedule a job
type JobRequest struct {
	CallID               string            `json:"call_id,omitempty"`
	URL                  string            `json:"url" validate:"required"`
	Method               string            `json:"method" validate:"required"`
	Headers              map[string]string `json:"headers,omitempty"`
	Body                 string            `json:"body,omitempty"`
	QueueName            string            `json:"queue_name,omitempty"`
	Priority             int               `json:"priority,omitempty"`
	MaxRetries           int               `json:"max_retries,omitempty"`
	ScheduledAt          *time.Time        `json:"scheduled_at,omitempty"`
	WebhookURL           string            `json:"webhook_url,omitempty"`
	RequestedConcurrency int               `json:"concurrency,omitempty"` // NEW: Dedicated workers for this queue
	CompletionMode       string            `json:"completion_mode,omitempty"`
}

// QueueStats represents statistics for a queue (compatible with Asynq)
type QueueStats struct {
	QueueName     string `json:"queue_name"`
	PendingJobs   int64  `json:"pending_jobs"`
	ActiveJobs    int64  `json:"active_jobs"`
	ScheduledJobs int64  `json:"scheduled_jobs"`
	RetryJobs     int64  `json:"retry_jobs"`
	ArchivedJobs  int64  `json:"archived_jobs"`
	CompletedJobs int64  `json:"completed_jobs"`
	FailedJobs    int64  `json:"failed_jobs"`
	ProcessedJobs int64  `json:"processed_jobs"`
	WorkersActive int    `json:"workers_active"`
}

// WebhookPayload represents the payload sent to webhook URLs
type WebhookPayload struct {
	CallID      string       `json:"call_id"`
	JobID       string       `json:"job_id"`
	Status      JobStatus    `json:"status"`
	Response    *JobResponse `json:"response,omitempty"`
	Error       string       `json:"error,omitempty"`
	CompletedAt time.Time    `json:"completed_at"`
	Attempts    int          `json:"attempts"`
}

// WebhookConfirmation represents webhook confirmation response
type WebhookConfirmation struct {
	CallID  string     `json:"call_id"`
	JobID   string     `json:"job_id"`
	Status  string     `json:"status"` // "success", "retry", "fail"
	Message string     `json:"message,omitempty"`
	RetryAt *time.Time `json:"retry_at,omitempty"`
}

// NewJob creates a new job with default values
func NewJob(req *JobRequest) *Job {
	now := time.Now()

	job := &Job{
		ID:                   uuid.New().String(),
		CallID:               req.CallID,
		URL:                  req.URL,
		Method:               req.Method,
		Headers:              req.Headers,
		Body:                 req.Body,
		QueueName:            req.QueueName,
		Status:               JobStatusPending,
		Priority:             req.Priority,
		Retries:              0,
		MaxRetries:           req.MaxRetries,
		RequestedConcurrency: req.RequestedConcurrency, // NEW: Store requested concurrency
		CreatedAt:            now,
		UpdatedAt:            now,
		WebhookURL:           req.WebhookURL,
		CompletionMode:       req.CompletionMode,
	}

	// Set defaults
	if job.CallID == "" {
		job.CallID = job.ID
	}
	if job.QueueName == "" {
		job.QueueName = "default"
	}
	if job.Method == "" {
		job.Method = "GET"
	}
	if job.MaxRetries == 0 {
		job.MaxRetries = 3
	}
	if req.ScheduledAt != nil {
		job.ScheduledAt = *req.ScheduledAt
	} else {
		job.ScheduledAt = now
	}
	if job.CompletionMode == "" {
		if job.WebhookURL != "" {
			job.CompletionMode = "webhook" // Default to webhook if webhook URL provided
		} else {
			job.CompletionMode = "immediate" // Default to immediate if no webhook URL
		}
	}

	return job
}

// ToJSON converts job to JSON string
func (j *Job) ToJSON() (string, error) {
	data, err := json.Marshal(j)
	return string(data), err
}

// FromJSON creates job from JSON string
func JobFromJSON(data string) (*Job, error) {
	var job Job
	err := json.Unmarshal([]byte(data), &job)
	return &job, err
}

// IsRetryable checks if job can be retried
func (j *Job) IsRetryable() bool {
	return j.Retries < j.MaxRetries && (j.Status == JobStatusFailed || j.Status == JobStatusRetrying)
}

// CanRetry checks if job can be retried (alias for IsRetryable for compatibility)
func (j *Job) CanRetry() bool {
	return j.IsRetryable()
}

// ShouldProcess checks if job should be processed now
func (j *Job) ShouldProcess() bool {
	return j.Status == JobStatusPending && time.Now().After(j.ScheduledAt)
}

// MarkAsProcessing marks job as being processed
func (j *Job) MarkAsProcessing() {
	now := time.Now()
	j.Status = JobStatusProcessing
	j.StartedAt = &now
	j.UpdatedAt = now
	j.Retries++
}

// MarkAsAwaitingWebhook marks job as awaiting webhook confirmation
func (j *Job) MarkAsAwaitingWebhook(response *JobResponse) {
	now := time.Now()
	j.Status = JobStatusAwaitingHook
	j.Response = response
	j.UpdatedAt = now
}

// MarkAsCompleted marks job as completed
func (j *Job) MarkAsCompleted(response *JobResponse) {
	now := time.Now()
	j.Status = JobStatusCompleted
	if response != nil {
		j.Response = response
	}
	j.CompletedAt = &now
	j.UpdatedAt = now
}

// MarkAsFailed marks job as failed
func (j *Job) MarkAsFailed(errorMsg string) {
	now := time.Now()
	j.Status = JobStatusFailed
	j.Error = errorMsg
	j.CompletedAt = &now
	j.UpdatedAt = now
}

// MarkForRetry marks job for retry
func (j *Job) MarkForRetry(errorMsg string, backoffDelay time.Duration) {
	now := time.Now()
	j.Status = JobStatusRetrying
	j.Error = errorMsg
	j.ScheduledAt = now.Add(backoffDelay)
	j.UpdatedAt = now
}

// GetAsynqPayload returns the job data as Asynq task payload
func (j *Job) GetAsynqPayload() ([]byte, error) {
	return json.Marshal(j)
}

// CreateFromAsynqPayload creates a job from Asynq task payload
func CreateFromAsynqPayload(payload []byte) (*Job, error) {
	var job Job
	err := json.Unmarshal(payload, &job)
	return &job, err
}
