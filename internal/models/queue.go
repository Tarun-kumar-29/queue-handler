package models

import (
	"encoding/json"
	"fmt"
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

// Job represents a generic HTTP job in the queue (compatible with Asynq)
type Job struct {
	ID                   string            `json:"id"`
	RequestID            string            `json:"request_id"` // Generalized from CallID
	URL                  string            `json:"url"`
	Method               string            `json:"method"`
	Headers              map[string]string `json:"headers,omitempty"`
	Body                 string            `json:"body,omitempty"`
	QueueName            string            `json:"queue_name"`
	Status               JobStatus         `json:"status"`
	Priority             int               `json:"priority"`
	Retries              int               `json:"retries"`
	MaxRetries           int               `json:"max_retries"`
	RequestedConcurrency int               `json:"requested_concurrency,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	ScheduledAt          time.Time         `json:"scheduled_at,omitempty"`
	StartedAt            *time.Time        `json:"started_at,omitempty"`
	CompletedAt          *time.Time        `json:"completed_at,omitempty"`
	Error                string            `json:"error,omitempty"`
	Response             *JobResponse      `json:"response,omitempty"`
	WebhookURL           string            `json:"webhook_url,omitempty"`
	CompletionMode       string            `json:"completion_mode"` // "immediate" or "webhook"

	// Generic metadata for flexible use cases
	Metadata map[string]interface{} `json:"metadata,omitempty"` // Replaces call-specific fields

	// Asynq specific fields
	TaskID    string                 `json:"task_id,omitempty"`
	AsynqInfo map[string]interface{} `json:"asynq_info,omitempty"`
}

type ClearQueueRequest struct {
	QueueName string `json:"queue_name" validate:"required"`
}

type ClearQueueResponse struct {
	Success     bool   `json:"success"`
	QueueName   string `json:"queue_name"`
	Message     string `json:"message"`
	DeletedJobs int    `json:"deleted_jobs,omitempty"`
}

// BulkJobItem represents a single job in a bulk scheduling request
type BulkJobItem struct {
	// Core job identification (generic)
	JobID     string `json:"job_id,omitempty"`     // Optional custom job ID
	RequestID string `json:"request_id,omitempty"` // Generalized from CallID

	// HTTP request details
	URL     string            `json:"url" validate:"required"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`

	// Queue configuration
	QueueName            string     `json:"queue_name,omitempty"`
	Priority             int        `json:"priority,omitempty"`
	MaxRetries           int        `json:"max_retries,omitempty"`
	ScheduledAt          *time.Time `json:"scheduled_at,omitempty"`
	WebhookURL           string     `json:"webhook_url,omitempty"`
	RequestedConcurrency int        `json:"concurrency,omitempty"`
	CompletionMode       string     `json:"completion_mode,omitempty"`

	// Generic metadata for flexible identification and grouping
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Backward compatibility fields (optional - can be removed if not needed)
	AgentID    string `json:"agent_id,omitempty"`    // Moved to metadata
	ProspectID string `json:"prospect_id,omitempty"` // Moved to metadata
	CallID     string `json:"call_id,omitempty"`     // Moved to request_id
}

// BulkJobRequest represents a request to schedule multiple jobs
type BulkJobRequest struct {
	Jobs []BulkJobItem `json:"jobs" validate:"required,min=1,max=100"`

	// Global defaults that apply to all jobs if not specified per job
	DefaultQueueName      string                 `json:"default_queue_name,omitempty"`
	DefaultMethod         string                 `json:"default_method,omitempty"`
	DefaultHeaders        map[string]string      `json:"default_headers,omitempty"`
	DefaultMaxRetries     int                    `json:"default_max_retries,omitempty"`
	DefaultWebhookURL     string                 `json:"default_webhook_url,omitempty"`
	DefaultConcurrency    int                    `json:"default_concurrency,omitempty"`
	DefaultCompletionMode string                 `json:"default_completion_mode,omitempty"`
	DefaultMetadata       map[string]interface{} `json:"default_metadata,omitempty"`
}

// BulkJobResult represents the result of scheduling a single job in bulk
type BulkJobResult struct {
	Success   bool   `json:"success"`
	JobID     string `json:"job_id,omitempty"`
	RequestID string `json:"request_id,omitempty"` // Generalized from CallID
	TaskID    string `json:"task_id,omitempty"`
	QueueName string `json:"queue_name,omitempty"`
	Error     string `json:"error,omitempty"`
	Index     int    `json:"index"` // Position in the original array

	// Include key metadata for identification
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Backward compatibility
	CallID     string `json:"call_id,omitempty"`     // For backward compatibility
	AgentID    string `json:"agent_id,omitempty"`    // For backward compatibility
	ProspectID string `json:"prospect_id,omitempty"` // For backward compatibility
}

// BulkJobResponse represents the response from bulk job scheduling
type BulkJobResponse struct {
	Success        bool            `json:"success"`
	TotalJobs      int             `json:"total_jobs"`
	Successful     int             `json:"successful"`
	Failed         int             `json:"failed"`
	Results        []BulkJobResult `json:"results"`
	Message        string          `json:"message"`
	ProcessingTime string          `json:"processing_time"`
	QueueSummary   map[string]int  `json:"queue_summary"` // Count of jobs per queue
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
	JobID                string                 `json:"job_id,omitempty"`     // Optional custom job ID
	RequestID            string                 `json:"request_id,omitempty"` // Generalized from CallID
	URL                  string                 `json:"url" validate:"required"`
	Method               string                 `json:"method" validate:"required"`
	Headers              map[string]string      `json:"headers,omitempty"`
	Body                 string                 `json:"body,omitempty"`
	QueueName            string                 `json:"queue_name,omitempty"`
	Priority             int                    `json:"priority,omitempty"`
	MaxRetries           int                    `json:"max_retries,omitempty"`
	ScheduledAt          *time.Time             `json:"scheduled_at,omitempty"`
	WebhookURL           string                 `json:"webhook_url,omitempty"`
	RequestedConcurrency int                    `json:"concurrency,omitempty"`
	CompletionMode       string                 `json:"completion_mode,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`

	// Backward compatibility
	CallID string `json:"call_id,omitempty"` // Maps to request_id
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
	RequestID   string                 `json:"request_id"` // Generalized from CallID
	JobID       string                 `json:"job_id"`
	Status      JobStatus              `json:"status"`
	Response    *JobResponse           `json:"response,omitempty"`
	Error       string                 `json:"error,omitempty"`
	CompletedAt time.Time              `json:"completed_at"`
	Attempts    int                    `json:"attempts"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`

	// Backward compatibility
	CallID string `json:"call_id,omitempty"` // For backward compatibility
}

// WebhookConfirmation represents webhook confirmation response
type WebhookConfirmation struct {
	RequestID string     `json:"request_id"` // Generalized from CallID
	JobID     string     `json:"job_id"`
	Status    string     `json:"status"` // "success", "retry", "fail"
	Message   string     `json:"message,omitempty"`
	RetryAt   *time.Time `json:"retry_at,omitempty"`

	// Backward compatibility
	CallID string `json:"call_id,omitempty"` // For backward compatibility
}

// NewJob creates a new job with default values
func NewJob(req *JobRequest) *Job {
	now := time.Now()

	job := &Job{
		ID:                   uuid.New().String(),
		RequestID:            req.RequestID,
		URL:                  req.URL,
		Method:               req.Method,
		Headers:              req.Headers,
		Body:                 req.Body,
		QueueName:            req.QueueName,
		Status:               JobStatusPending,
		Priority:             req.Priority,
		Retries:              0,
		MaxRetries:           req.MaxRetries,
		RequestedConcurrency: req.RequestedConcurrency,
		CreatedAt:            now,
		UpdatedAt:            now,
		WebhookURL:           req.WebhookURL,
		CompletionMode:       req.CompletionMode,
		Metadata:             req.Metadata,
	}

	// Handle backward compatibility
	if job.RequestID == "" && req.CallID != "" {
		job.RequestID = req.CallID
	}

	// Set defaults
	if job.RequestID == "" {
		job.RequestID = job.ID
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
			job.CompletionMode = "webhook"
		} else {
			job.CompletionMode = "immediate"
		}
	}

	// Initialize metadata if nil
	if job.Metadata == nil {
		job.Metadata = make(map[string]interface{})
	}

	// Set a custom job ID if provided
	if req.JobID != "" {
		job.ID = req.JobID
	}

	return job
}

// ToJobRequest converts a BulkJobItem to a JobRequest
func (item *BulkJobItem) ToJobRequest(defaults *BulkJobRequest) *JobRequest {
	req := &JobRequest{
		JobID:                item.JobID,
		RequestID:            item.RequestID,
		URL:                  item.URL,
		Method:               item.Method,
		Headers:              item.Headers,
		Body:                 item.Body,
		QueueName:            item.QueueName,
		Priority:             item.Priority,
		MaxRetries:           item.MaxRetries,
		ScheduledAt:          item.ScheduledAt,
		WebhookURL:           item.WebhookURL,
		RequestedConcurrency: item.RequestedConcurrency,
		CompletionMode:       item.CompletionMode,
		Metadata:             item.Metadata,
	}

	// Handle backward compatibility
	if req.RequestID == "" && item.CallID != "" {
		req.RequestID = item.CallID
	}

	// Initialize metadata if nil
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}

	// Move legacy fields to metadata for backward compatibility
	if item.AgentID != "" {
		req.Metadata["agent_id"] = item.AgentID
	}
	if item.ProspectID != "" {
		req.Metadata["prospect_id"] = item.ProspectID
	}

	// Apply defaults if not specified
	if defaults != nil {
		if req.Method == "" && defaults.DefaultMethod != "" {
			req.Method = defaults.DefaultMethod
		}
		if req.QueueName == "" && defaults.DefaultQueueName != "" {
			req.QueueName = defaults.DefaultQueueName
		}
		if req.MaxRetries == 0 && defaults.DefaultMaxRetries > 0 {
			req.MaxRetries = defaults.DefaultMaxRetries
		}
		if req.WebhookURL == "" && defaults.DefaultWebhookURL != "" {
			req.WebhookURL = defaults.DefaultWebhookURL
		}
		if req.RequestedConcurrency == 0 && defaults.DefaultConcurrency > 0 {
			req.RequestedConcurrency = defaults.DefaultConcurrency
		}
		if req.CompletionMode == "" && defaults.DefaultCompletionMode != "" {
			req.CompletionMode = defaults.DefaultCompletionMode
		}

		// Merge headers
		if len(defaults.DefaultHeaders) > 0 {
			if req.Headers == nil {
				req.Headers = make(map[string]string)
			}
			for k, v := range defaults.DefaultHeaders {
				if _, exists := req.Headers[k]; !exists {
					req.Headers[k] = v
				}
			}
		}

		// Merge metadata
		if len(defaults.DefaultMetadata) > 0 {
			for k, v := range defaults.DefaultMetadata {
				if _, exists := req.Metadata[k]; !exists {
					req.Metadata[k] = v
				}
			}
		}
	}

	// Generate RequestID if not provided
	if req.RequestID == "" {
		// Use metadata for unique ID generation if available
		if agentID, ok := req.Metadata["agent_id"].(string); ok {
			if prospectID, ok := req.Metadata["prospect_id"].(string); ok {
				req.RequestID = fmt.Sprintf("%s_%s_%s", agentID, prospectID, uuid.New().String()[:8])
			} else {
				req.RequestID = fmt.Sprintf("%s_%s", agentID, uuid.New().String()[:8])
			}
		} else {
			req.RequestID = uuid.New().String()
		}
	}

	return req
}

// GetMetadataString safely gets a string value from metadata
func (j *Job) GetMetadataString(key string) string {
	if j.Metadata == nil {
		return ""
	}
	if val, ok := j.Metadata[key].(string); ok {
		return val
	}
	return ""
}

// SetMetadata safely sets a metadata value
func (j *Job) SetMetadata(key string, value interface{}) {
	if j.Metadata == nil {
		j.Metadata = make(map[string]interface{})
	}
	j.Metadata[key] = value
}

// GetCallID returns the CallID for backward compatibility
func (j *Job) GetCallID() string {
	return j.RequestID
}

// GetAgentID returns agent_id from metadata for backward compatibility
func (j *Job) GetAgentID() string {
	return j.GetMetadataString("agent_id")
}

// GetProspectID returns prospect_id from metadata for backward compatibility
func (j *Job) GetProspectID() string {
	return j.GetMetadataString("prospect_id")
}

// Existing methods remain the same...
func (j *Job) ToJSON() (string, error) {
	data, err := json.Marshal(j)
	return string(data), err
}

func JobFromJSON(data string) (*Job, error) {
	var job Job
	err := json.Unmarshal([]byte(data), &job)
	return &job, err
}

func (j *Job) IsRetryable() bool {
	return j.Retries < j.MaxRetries && (j.Status == JobStatusFailed || j.Status == JobStatusRetrying)
}

func (j *Job) CanRetry() bool {
	return j.IsRetryable()
}

func (j *Job) ShouldProcess() bool {
	return j.Status == JobStatusPending && time.Now().After(j.ScheduledAt)
}

func (j *Job) MarkAsProcessing() {
	now := time.Now()
	j.Status = JobStatusProcessing
	j.StartedAt = &now
	j.UpdatedAt = now
	j.Retries++
}

func (j *Job) MarkAsAwaitingWebhook(response *JobResponse) {
	now := time.Now()
	j.Status = JobStatusAwaitingHook
	j.Response = response
	j.UpdatedAt = now
}

func (j *Job) MarkAsCompleted(response *JobResponse) {
	now := time.Now()
	j.Status = JobStatusCompleted
	if response != nil {
		j.Response = response
	}
	j.CompletedAt = &now
	j.UpdatedAt = now
}

func (j *Job) MarkAsFailed(errorMsg string) {
	now := time.Now()
	j.Status = JobStatusFailed
	j.Error = errorMsg
	j.CompletedAt = &now
	j.UpdatedAt = now
}

func (j *Job) MarkForRetry(errorMsg string, backoffDelay time.Duration) {
	now := time.Now()
	j.Status = JobStatusRetrying
	j.Error = errorMsg
	j.ScheduledAt = now.Add(backoffDelay)
	j.UpdatedAt = now
}

func (j *Job) GetAsynqPayload() ([]byte, error) {
	return json.Marshal(j)
}

func CreateFromAsynqPayload(payload []byte) (*Job, error) {
	var job Job
	err := json.Unmarshal(payload, &job)
	return &job, err
}
