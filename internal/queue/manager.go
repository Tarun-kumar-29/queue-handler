package queue

import (
	"fmt"
	"log"
	"sync"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/models"
	"queue-handler-2/pkg/redis"

	"github.com/hibiken/asynq"
)

const (
	TaskTypeHTTPRequest = "http:request"
)

// QueueInfo tracks queue state and worker assignment
type QueueInfo struct {
	Name        string
	Concurrency int
	IsActive    bool
	LastJobAt   time.Time
	WorkerCount int
}

// Manager handles queue operations with dedicated workers per queue
type Manager struct {
	client      *asynq.Client
	inspector   *asynq.Inspector
	RedisClient *redis.Client
	config      *config.Config

	// Track active queues and their dedicated workers
	activeQueues map[string]*QueueInfo
	queueMutex   sync.RWMutex

	// Callback to notify worker manager about queue changes
	onQueueStart func(queueName string, concurrency int) error
	onQueueStop  func(queueName string) error
}

// NewManager creates a new queue manager with dedicated worker support
func NewManager(redisClient *redis.Client, config *config.Config) *Manager {
	asynqOpt := redisClient.GetAsynqOptions()

	client := asynq.NewClient(asynqOpt)
	inspector := asynq.NewInspector(asynqOpt)

	manager := &Manager{
		client:       client,
		inspector:    inspector,
		RedisClient:  redisClient,
		config:       config,
		activeQueues: make(map[string]*QueueInfo),
	}

	// Start queue monitor for auto-shutdown
	go manager.monitorQueues()

	log.Println("✅ Enhanced Queue manager initialized with dedicated worker support")
	return manager
}

// SetWorkerCallbacks sets callbacks for worker lifecycle management
func (m *Manager) SetWorkerCallbacks(onStart, onStop func(string, int) error) {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	m.onQueueStart = func(queueName string, concurrency int) error {
		return onStart(queueName, concurrency)
	}
	m.onQueueStop = func(queueName string) error {
		return onStop(queueName, 0) // 0 concurrency means stop
	}
}

// EnqueueJob adds a job to queue with optional dedicated worker management
func (m *Manager) EnqueueJob(job *models.Job) error {
	// Store job metadata in Redis for tracking
	jobData, err := job.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize job: %w", err)
	}

	if err := m.RedisClient.StoreJobMetadata(job.ID, jobData); err != nil {
		return fmt.Errorf("failed to store job metadata: %w", err)
	}

	// Create RequestID mapping (backward compatible with CallID)
	if err := m.RedisClient.CreateCallIDMapping(job.RequestID, job.ID); err != nil {
		return fmt.Errorf("failed to create request_id mapping: %w", err)
	}

	// Ensure dedicated workers are running for this queue
	if err := m.ensureQueueWorkers(job.QueueName, job.RequestedConcurrency); err != nil {
		return fmt.Errorf("failed to ensure queue workers: %w", err)
	}

	// Create Asynq task
	payload, err := job.GetAsynqPayload()
	if err != nil {
		return fmt.Errorf("failed to create task payload: %w", err)
	}

	task := asynq.NewTask(TaskTypeHTTPRequest, payload)

	// Prepare Asynq options for dedicated queue
	var opts []asynq.Option
	opts = append(opts, asynq.Queue(job.QueueName))
	opts = append(opts, asynq.MaxRetry(job.MaxRetries))
	opts = append(opts, asynq.TaskID(job.ID))

	// Set scheduling
	if !job.ScheduledAt.IsZero() && job.ScheduledAt.After(time.Now()) {
		opts = append(opts, asynq.ProcessAt(job.ScheduledAt))
	}

	// Enqueue task
	info, err := m.client.Enqueue(task, opts...)
	if err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	// Update queue info
	m.updateQueueActivity(job.QueueName)

	// Update job with Asynq task info
	job.TaskID = info.ID
	if job.AsynqInfo == nil {
		job.AsynqInfo = make(map[string]interface{})
	}
	job.AsynqInfo["queue"] = info.Queue
	job.AsynqInfo["max_retry"] = info.MaxRetry

	// Update job metadata
	if err := m.updateJobMetadata(job); err != nil {
		log.Printf("⚠️ Failed to update job metadata: %v", err)
	}

	// Update statistics
	m.RedisClient.IncrementStat(job.QueueName, "total")
	m.RedisClient.IncrementStat(job.QueueName, "pending")

	log.Printf("📋 Job enqueued to dedicated queue: ID=%s, RequestID=%s, Queue=%s, Workers=%d",
		job.ID, job.RequestID, job.QueueName, m.getQueueConcurrency(job.QueueName))

	return nil
}

// ensureQueueWorkers ensures dedicated workers are running for the queue
func (m *Manager) ensureQueueWorkers(queueName string, requestedConcurrency int) error {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	log.Printf("🔍 DEBUG: ensureQueueWorkers called for %s, requested concurrency: %d", queueName, requestedConcurrency)

	queueInfo, exists := m.activeQueues[queueName]
	log.Printf("🔍 DEBUG: Queue %s exists: %v", queueName, exists)
	if exists {
		log.Printf("🔍 DEBUG: Queue %s isActive: %v, concurrency: %d", queueName, queueInfo.IsActive, queueInfo.Concurrency)
	}

	// Determine concurrency to use
	concurrency := requestedConcurrency
	if concurrency == 0 {
		concurrency = m.config.DefaultConcurrency
	}
	log.Printf("🔍 DEBUG: Final concurrency to use: %d", concurrency)

	// Check both !exists AND !IsActive
	if !exists || !queueInfo.IsActive {
		log.Printf("🔍 DEBUG: Need to start workers - exists: %v, isActive: %v", exists, exists && queueInfo.IsActive)

		if !exists {
			// Start new dedicated workers for this queue
			queueInfo = &QueueInfo{
				Name:        queueName,
				Concurrency: concurrency,
				IsActive:    true,
				LastJobAt:   time.Now(),
				WorkerCount: concurrency,
			}
			m.activeQueues[queueName] = queueInfo
			log.Printf("🔍 DEBUG: Created new queueInfo for %s", queueName)
		} else {
			// Reactivate existing queue info
			log.Printf("🔍 DEBUG: Reactivating existing queue %s", queueName)
			queueInfo.IsActive = true
			queueInfo.Concurrency = concurrency
			queueInfo.LastJobAt = time.Now()
			queueInfo.WorkerCount = concurrency
		}

		// Start dedicated workers
		if m.onQueueStart != nil {
			log.Printf("🔍 DEBUG: Calling onQueueStart for %s with concurrency %d", queueName, concurrency)
			if err := m.onQueueStart(queueName, concurrency); err != nil {
				log.Printf("❌ DEBUG: onQueueStart failed: %v", err)
				if exists {
					queueInfo.IsActive = false
				} else {
					delete(m.activeQueues, queueName)
				}
				return fmt.Errorf("failed to start workers for queue %s: %w", queueName, err)
			}
			log.Printf("✅ DEBUG: onQueueStart succeeded for %s", queueName)
		} else {
			log.Printf("❌ ERROR: onQueueStart callback is nil!")
			return fmt.Errorf("worker callback not set")
		}

		log.Printf("🚀 Started dedicated workers: Queue=%s, Workers=%d", queueName, concurrency)
	} else {
		log.Printf("🔍 DEBUG: Queue %s already active, just updating activity", queueName)
		// Update existing queue activity
		queueInfo.LastJobAt = time.Now()

		// Scale workers if different concurrency requested
		if requestedConcurrency > 0 && requestedConcurrency != queueInfo.Concurrency {
			log.Printf("📈 Scaling queue %s from %d to %d workers",
				queueName, queueInfo.Concurrency, concurrency)
			queueInfo.Concurrency = concurrency
			queueInfo.WorkerCount = concurrency

			// Restart workers with new concurrency
			if m.onQueueStop != nil {
				log.Printf("🔍 DEBUG: Stopping workers for scaling")
				m.onQueueStop(queueName)
			}
			if m.onQueueStart != nil {
				log.Printf("🔍 DEBUG: Restarting workers with new concurrency")
				if err := m.onQueueStart(queueName, concurrency); err != nil {
					return fmt.Errorf("failed to scale workers for queue %s: %w", queueName, err)
				}
			}
		}
	}

	log.Printf("🔍 DEBUG: ensureQueueWorkers completed for %s", queueName)
	return nil
}

// EnqueueBulkJobs schedules multiple jobs efficiently with generalized logic
func (m *Manager) EnqueueBulkJobs(bulkRequest *models.BulkJobRequest) (*models.BulkJobResponse, error) {
	startTime := time.Now()

	response := &models.BulkJobResponse{
		TotalJobs:    len(bulkRequest.Jobs),
		Results:      make([]models.BulkJobResult, len(bulkRequest.Jobs)),
		QueueSummary: make(map[string]int),
	}

	log.Printf("📦 Starting bulk job scheduling: %d jobs", response.TotalJobs)

	// Process each job
	for i, jobItem := range bulkRequest.Jobs {
		result := models.BulkJobResult{
			Index:    i,
			Metadata: jobItem.Metadata,
		}

		// Handle backward compatibility
		if jobItem.AgentID != "" {
			result.AgentID = jobItem.AgentID
			if result.Metadata == nil {
				result.Metadata = make(map[string]interface{})
			}
			result.Metadata["agent_id"] = jobItem.AgentID
		}
		if jobItem.ProspectID != "" {
			result.ProspectID = jobItem.ProspectID
			if result.Metadata == nil {
				result.Metadata = make(map[string]interface{})
			}
			result.Metadata["prospect_id"] = jobItem.ProspectID
		}

		// Convert to JobRequest
		jobReq := jobItem.ToJobRequest(bulkRequest)

		// Validate required fields
		if jobReq.URL == "" {
			result.Success = false
			result.Error = "URL is required"
			response.Results[i] = result
			continue
		}

		// Set defaults
		if jobReq.Method == "" {
			jobReq.Method = "POST"
		}
		if jobReq.QueueName == "" {
			// Create queue name based on metadata for logical grouping
			queueName := m.generateQueueName(jobReq)
			jobReq.QueueName = queueName
		}
		if jobReq.MaxRetries == 0 {
			jobReq.MaxRetries = m.config.MaxRetries
		}

		// Create and enqueue job
		job := models.NewJob(jobReq)

		// Store metadata in job for tracking
		if job.AsynqInfo == nil {
			job.AsynqInfo = make(map[string]interface{})
		}

		// Copy all metadata to AsynqInfo for tracking
		if job.Metadata != nil {
			for k, v := range job.Metadata {
				job.AsynqInfo[k] = v
			}
		}

		if err := m.EnqueueJob(job); err != nil {
			result.Success = false
			result.Error = err.Error()
			log.Printf("❌ Bulk job %d failed: %v", i+1, err)
		} else {
			result.Success = true
			result.JobID = job.ID
			result.RequestID = job.RequestID
			result.TaskID = job.TaskID
			result.QueueName = job.QueueName
			result.CallID = job.RequestID // Backward compatibility
			response.Successful++

			// Update queue summary
			response.QueueSummary[job.QueueName]++

			// Log with metadata context
			metadataStr := ""
			if job.Metadata != nil {
				if agentID := job.GetMetadataString("agent_id"); agentID != "" {
					metadataStr += fmt.Sprintf("AgentID=%s, ", agentID)
				}
				if prospectID := job.GetMetadataString("prospect_id"); prospectID != "" {
					metadataStr += fmt.Sprintf("ProspectID=%s, ", prospectID)
				}
			}

			log.Printf("✅ Bulk job %d/%d: %sJobID=%s, Queue=%s",
				i+1, response.TotalJobs, metadataStr, job.ID, job.QueueName)
		}

		response.Results[i] = result
	}

	// Calculate final stats
	response.Failed = response.TotalJobs - response.Successful
	response.Success = response.Failed == 0
	response.ProcessingTime = time.Since(startTime).String()

	if response.Success {
		response.Message = fmt.Sprintf("All %d jobs scheduled successfully across %d queues",
			response.TotalJobs, len(response.QueueSummary))
	} else {
		response.Message = fmt.Sprintf("%d of %d jobs scheduled successfully (%d failed)",
			response.Successful, response.TotalJobs, response.Failed)
	}

	log.Printf("📦 Bulk scheduling completed: %s (took %s)", response.Message, response.ProcessingTime)

	// Log queue distribution
	log.Printf("📊 Queue distribution:")
	for queueName, count := range response.QueueSummary {
		log.Printf("   %s: %d jobs", queueName, count)
	}

	return response, nil
}

// generateQueueName creates a logical queue name based on job metadata
func (m *Manager) generateQueueName(jobReq *models.JobRequest) string {
	if jobReq.Metadata == nil {
		return "default"
	}

	// Strategy 1: Use agent_id for call-related jobs (backward compatibility)
	if agentID, ok := jobReq.Metadata["agent_id"].(string); ok && agentID != "" {
		if len(agentID) > 8 {
			return fmt.Sprintf("agent_%s", agentID[:8])
		}
		return fmt.Sprintf("agent_%s", agentID)
	}

	// Strategy 2: Use user_id for user-specific jobs
	if userID, ok := jobReq.Metadata["user_id"].(string); ok && userID != "" {
		if len(userID) > 8 {
			return fmt.Sprintf("user_%s", userID[:8])
		}
		return fmt.Sprintf("user_%s", userID)
	}

	// Strategy 3: Use service_name for service-specific jobs
	if serviceName, ok := jobReq.Metadata["service_name"].(string); ok && serviceName != "" {
		return fmt.Sprintf("service_%s", serviceName)
	}

	// Strategy 4: Use job_type for type-specific jobs
	if jobType, ok := jobReq.Metadata["job_type"].(string); ok && jobType != "" {
		return fmt.Sprintf("type_%s", jobType)
	}

	// Strategy 5: Use tenant_id for multi-tenant applications
	if tenantID, ok := jobReq.Metadata["tenant_id"].(string); ok && tenantID != "" {
		if len(tenantID) > 8 {
			return fmt.Sprintf("tenant_%s", tenantID[:8])
		}
		return fmt.Sprintf("tenant_%s", tenantID)
	}

	// Strategy 6: Use priority-based queues
	if priority, ok := jobReq.Metadata["priority"].(string); ok && priority != "" {
		return fmt.Sprintf("priority_%s", priority)
	}

	// Default fallback
	return "default"
}

// monitorQueues monitors queue activity and shuts down idle workers
func (m *Manager) monitorQueues() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for range ticker.C {
		m.checkIdleQueues()
	}
}

// checkIdleQueues shuts down workers for empty queues
func (m *Manager) checkIdleQueues() {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	idleTimeout := 2 * time.Minute // Shutdown after 2 minutes of inactivity

	for queueName, queueInfo := range m.activeQueues {
		if !queueInfo.IsActive {
			continue
		}

		// Check if queue has been idle too long
		if time.Since(queueInfo.LastJobAt) > idleTimeout {
			// Check if queue is actually empty
			if m.isQueueEmpty(queueName) {
				log.Printf("🛑 Shutting down idle queue: %s (idle for %v)",
					queueName, time.Since(queueInfo.LastJobAt))

				// Shutdown workers
				if m.onQueueStop != nil {
					m.onQueueStop(queueName)
				}

				queueInfo.IsActive = false
				queueInfo.WorkerCount = 0
			} else {
				// Queue has jobs, update activity
				queueInfo.LastJobAt = time.Now()
			}
		}
	}
}

// Rest of the methods remain the same but with updated logging...
// [Previous methods like ClearQueue, isQueueEmpty, updateQueueActivity, etc. remain the same
//  but with RequestID instead of CallID in logging messages]

// ClearQueue removes all jobs from a specific queue
func (m *Manager) ClearQueue(queueName string) (int, error) {
	deletedCount := 0

	// Get queue info to check job counts
	queueInfo, err := m.inspector.GetQueueInfo(queueName)
	if err != nil {
		return 0, fmt.Errorf("failed to get queue info: %w", err)
	}

	totalJobs := queueInfo.Pending + queueInfo.Active + queueInfo.Scheduled +
		queueInfo.Retry + queueInfo.Archived

	if totalJobs == 0 {
		return 0, nil // Queue is already empty
	}

	// Delete pending jobs
	if queueInfo.Pending > 0 {
		count, err := m.inspector.DeleteAllPendingTasks(queueName)
		if err != nil {
			log.Printf("⚠️ Failed to delete pending tasks: %v", err)
		} else {
			deletedCount += count
		}
	}

	// Delete scheduled jobs
	if queueInfo.Scheduled > 0 {
		count, err := m.inspector.DeleteAllScheduledTasks(queueName)
		if err != nil {
			log.Printf("⚠️ Failed to delete scheduled tasks: %v", err)
		} else {
			deletedCount += count
		}
	}

	// Delete retry jobs
	if queueInfo.Retry > 0 {
		count, err := m.inspector.DeleteAllRetryTasks(queueName)
		if err != nil {
			log.Printf("⚠️ Failed to delete retry tasks: %v", err)
		} else {
			deletedCount += count
		}
	}

	// Delete archived jobs
	if queueInfo.Archived > 0 {
		count, err := m.inspector.DeleteAllArchivedTasks(queueName)
		if err != nil {
			log.Printf("⚠️ Failed to delete archived tasks: %v", err)
		} else {
			deletedCount += count
		}
	}

	// Note: Active jobs cannot be deleted as they are currently being processed
	if queueInfo.Active > 0 {
		log.Printf("⚠️ Queue %s has %d active jobs that cannot be cleared", queueName, queueInfo.Active)
	}

	// Clear queue metadata
	m.queueMutex.Lock()
	if queueInfo, exists := m.activeQueues[queueName]; exists {
		queueInfo.LastJobAt = time.Now()
	}
	m.queueMutex.Unlock()

	log.Printf("🧹 Cleared queue '%s': deleted %d jobs (%d active jobs remain)",
		queueName, deletedCount, queueInfo.Active)

	return deletedCount, nil
}

// isQueueEmpty checks if a queue has no pending or active jobs
func (m *Manager) isQueueEmpty(queueName string) bool {
	queueInfo, err := m.inspector.GetQueueInfo(queueName)
	if err != nil {
		return true // Assume empty if we can't check
	}

	totalJobs := queueInfo.Pending + queueInfo.Active + queueInfo.Scheduled + queueInfo.Retry
	return totalJobs == 0
}

// updateQueueActivity updates the last activity time for a queue
func (m *Manager) updateQueueActivity(queueName string) {
	m.queueMutex.RLock()
	if queueInfo, exists := m.activeQueues[queueName]; exists {
		queueInfo.LastJobAt = time.Now()
	}
	m.queueMutex.RUnlock()
}

// getQueueConcurrency returns the current concurrency for a queue
func (m *Manager) getQueueConcurrency(queueName string) int {
	m.queueMutex.RLock()
	defer m.queueMutex.RUnlock()

	if queueInfo, exists := m.activeQueues[queueName]; exists {
		return queueInfo.Concurrency
	}
	return m.config.DefaultConcurrency
}

// GetActiveQueues returns information about all active queues
func (m *Manager) GetActiveQueues() map[string]*QueueInfo {
	m.queueMutex.RLock()
	defer m.queueMutex.RUnlock()

	result := make(map[string]*QueueInfo)
	for name, info := range m.activeQueues {
		// Create a copy to avoid race conditions
		result[name] = &QueueInfo{
			Name:        info.Name,
			Concurrency: info.Concurrency,
			IsActive:    info.IsActive,
			LastJobAt:   info.LastJobAt,
			WorkerCount: info.WorkerCount,
		}
	}
	return result
}

// ForceShutdownQueue manually shuts down workers for a specific queue
func (m *Manager) ForceShutdownQueue(queueName string) error {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	if queueInfo, exists := m.activeQueues[queueName]; exists && queueInfo.IsActive {
		log.Printf("🛑 Force shutting down queue: %s", queueName)

		if m.onQueueStop != nil {
			if err := m.onQueueStop(queueName); err != nil {
				return err
			}
		}

		queueInfo.IsActive = false
		queueInfo.WorkerCount = 0
		return nil
	}

	return fmt.Errorf("queue %s is not active", queueName)
}

// RestartQueue manually restarts workers for a specific queue
func (m *Manager) RestartQueue(queueName string, concurrency int) error {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	if concurrency == 0 {
		concurrency = m.config.DefaultConcurrency
	}

	queueInfo, exists := m.activeQueues[queueName]
	if !exists {
		queueInfo = &QueueInfo{
			Name:        queueName,
			Concurrency: concurrency,
		}
		m.activeQueues[queueName] = queueInfo
	}

	// Stop existing workers if any
	if queueInfo.IsActive && m.onQueueStop != nil {
		m.onQueueStop(queueName)
	}

	// Start new workers
	if m.onQueueStart != nil {
		if err := m.onQueueStart(queueName, concurrency); err != nil {
			return err
		}
	}

	queueInfo.IsActive = true
	queueInfo.Concurrency = concurrency
	queueInfo.WorkerCount = concurrency
	queueInfo.LastJobAt = time.Now()

	log.Printf("🔄 Restarted queue: %s with %d workers", queueName, concurrency)
	return nil
}

// GetJob returns information about a specific job
func (m *Manager) GetJob(jobID string) (*models.Job, error) {
	jobData, err := m.RedisClient.GetJobMetadata(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job metadata: %w", err)
	}
	return models.JobFromJSON(jobData)
}

// GetJobByRequestID returns information about a job by RequestID (backward compatible with CallID)
func (m *Manager) GetJobByRequestID(requestID string) (*models.Job, error) {
	jobID, err := m.RedisClient.GetJobIDByCallID(requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job by request_id: %w", err)
	}
	return m.GetJob(jobID)
}

// GetJobByCallID returns information about a job by CallID (backward compatibility)
func (m *Manager) GetJobByCallID(callID string) (*models.Job, error) {
	return m.GetJobByRequestID(callID)
}

// UpdateJob updates job metadata
func (m *Manager) UpdateJob(job *models.Job) error {
	return m.updateJobMetadata(job)
}

// CompleteJob marks a job as completed and updates statistics
func (m *Manager) CompleteJob(job *models.Job) error {
	if err := m.updateJobMetadata(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	m.RedisClient.DecrementStat(job.QueueName, "processing")

	if job.Status == models.JobStatusCompleted {
		m.RedisClient.IncrementStat(job.QueueName, "completed")
		m.RedisClient.RemoveWebhookTracking(job.ID)
	} else {
		m.RedisClient.IncrementStat(job.QueueName, "failed")
	}

	// Update queue activity
	m.updateQueueActivity(job.QueueName)

	log.Printf("✅ Job completed: ID=%s, Status=%s", job.ID, job.Status)
	return nil
}

// CompleteJobAfterWebhook completes a job after webhook confirmation
func (m *Manager) CompleteJobAfterWebhook(jobID string) error {
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	if job.Status != models.JobStatusAwaitingHook {
		return fmt.Errorf("job %s is not awaiting webhook confirmation (status: %s)",
			jobID, job.Status)
	}

	job.MarkAsCompleted(nil)
	return m.CompleteJob(job)
}

// RetryJob puts a job back in the queue for retry
func (m *Manager) RetryJob(job *models.Job) error {
	// Let Asynq handle the retry automatically
	// We just update our job metadata
	return m.updateJobMetadata(job)
}

// RetryJobAfterWebhook retries a job based on webhook response
func (m *Manager) RetryJobAfterWebhook(jobID string, retryAt time.Time) error {
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	if !job.CanRetry() {
		job.MarkAsFailed("max retries exceeded after webhook retry request")
		return m.UpdateJob(job)
	}

	// Reset job for retry
	job.Status = models.JobStatusPending
	job.ScheduledAt = retryAt
	job.UpdatedAt = time.Now()
	job.StartedAt = nil
	job.Response = nil
	job.Error = ""

	// Re-enqueue the job
	return m.EnqueueJob(job)
}

// GetQueueStats returns statistics for a specific queue
func (m *Manager) GetQueueStats(queueName string) (*models.QueueStats, error) {
	queueInfo, err := m.inspector.GetQueueInfo(queueName)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue info from Asynq: %w", err)
	}

	stats := &models.QueueStats{
		QueueName:     queueName,
		PendingJobs:   int64(queueInfo.Pending),
		ActiveJobs:    int64(queueInfo.Active),
		ScheduledJobs: int64(queueInfo.Scheduled),
		RetryJobs:     int64(queueInfo.Retry),
		ArchivedJobs:  int64(queueInfo.Archived),
		ProcessedJobs: int64(queueInfo.Processed),
		FailedJobs:    int64(queueInfo.Failed),
		CompletedJobs: int64(queueInfo.Completed),
		WorkersActive: m.getQueueConcurrency(queueName),
	}

	return stats, nil
}

// ListQueues returns all queues
func (m *Manager) ListQueues() ([]string, error) {
	return m.inspector.Queues()
}

// GetWebhookPendingJobs returns jobs pending webhook confirmation
func (m *Manager) GetWebhookPendingJobs() ([]string, error) {
	return m.RedisClient.GetPendingWebhookJobs()
}

// DeleteJob removes a job completely
func (m *Manager) DeleteJob(jobID string) error {
	// Get job first
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	// Delete from Asynq if it has a task ID
	if job.TaskID != "" {
		if err := m.inspector.DeleteTask(job.QueueName, job.TaskID); err != nil {
			log.Printf("⚠️ Failed to delete task from Asynq: %v", err)
		}
	}

	// Remove job metadata
	if err := m.RedisClient.DeleteJobMetadata(jobID); err != nil {
		log.Printf("⚠️ Failed to delete job metadata: %v", err)
	}

	// Remove webhook tracking
	m.RedisClient.RemoveWebhookTracking(jobID)

	return nil
}

// ArchiveJob archives a job (using Asynq's archive functionality)
func (m *Manager) ArchiveJob(jobID string) error {
	// Get task info first
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	if job.TaskID == "" {
		return fmt.Errorf("job %s has no associated task ID", jobID)
	}

	// Archive the task in Asynq
	return m.inspector.ArchiveTask(job.QueueName, job.TaskID)
}

// PauseQueue pauses job processing for a queue
func (m *Manager) PauseQueue(queueName string) error {
	return m.inspector.PauseQueue(queueName)
}

// UnpauseQueue resumes job processing for a queue
func (m *Manager) UnpauseQueue(queueName string) error {
	return m.inspector.UnpauseQueue(queueName)
}

// Close shuts down the manager and all active queues
func (m *Manager) Close() error {
	// Shutdown all active queues
	m.queueMutex.Lock()
	for queueName, queueInfo := range m.activeQueues {
		if queueInfo.IsActive && m.onQueueStop != nil {
			m.onQueueStop(queueName)
		}
	}
	m.queueMutex.Unlock()

	if err := m.client.Close(); err != nil {
		log.Printf("Error closing Asynq client: %v", err)
	}
	if err := m.inspector.Close(); err != nil {
		log.Printf("Error closing Asynq inspector: %v", err)
	}
	return nil
}

// updateJobMetadata updates job metadata in Redis
func (m *Manager) updateJobMetadata(job *models.Job) error {
	job.UpdatedAt = time.Now()
	jobData, err := job.ToJSON()
	if err != nil {
		return err
	}
	return m.RedisClient.StoreJobMetadata(job.ID, jobData)
}
