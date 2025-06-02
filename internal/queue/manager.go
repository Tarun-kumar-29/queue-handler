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
	client       *asynq.Client
	inspector    *asynq.Inspector
	RedisClient  *redis.Client
	config       *config.Config
	
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

	// Create CallID mapping
	if err := m.RedisClient.CreateCallIDMapping(job.CallID, job.ID); err != nil {
		return fmt.Errorf("failed to create call_id mapping: %w", err)
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

	log.Printf("📋 Job enqueued to dedicated queue: ID=%s, CallID=%s, Queue=%s, Workers=%d", 
		job.ID, job.CallID, job.QueueName, m.getQueueConcurrency(job.QueueName))

	return nil
}

// ensureQueueWorkers ensures dedicated workers are running for the queue
func (m *Manager) ensureQueueWorkers(queueName string, requestedConcurrency int) error {
	m.queueMutex.Lock()
	defer m.queueMutex.Unlock()

	// DEBUG LOGS - Add these lines
	log.Printf("🔍 DEBUG: ensureQueueWorkers called for %s, requested concurrency: %d", queueName, requestedConcurrency)

	queueInfo, exists := m.activeQueues[queueName]
	log.Printf("🔍 DEBUG: Queue %s exists: %v", queueName, exists)
	if exists {
		log.Printf("🔍 DEBUG: Queue %s isActive: %v, concurrency: %d", queueName, queueInfo.IsActive, queueInfo.Concurrency)
	}
	log.Printf("🔍 DEBUG: onQueueStart callback is nil: %v", m.onQueueStart == nil)
	
	// Determine concurrency to use
	concurrency := requestedConcurrency
	if concurrency == 0 {
		concurrency = m.config.DefaultConcurrency
	}
	log.Printf("🔍 DEBUG: Final concurrency to use: %d", concurrency)

	// FIXED CONDITION: Check both !exists AND !IsActive
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

// Existing methods remain the same...
func (m *Manager) GetJob(jobID string) (*models.Job, error) {
	jobData, err := m.RedisClient.GetJobMetadata(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job metadata: %w", err)
	}
	return models.JobFromJSON(jobData)
}

func (m *Manager) GetJobByCallID(callID string) (*models.Job, error) {
	jobID, err := m.RedisClient.GetJobIDByCallID(callID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job by call_id: %w", err)
	}
	return m.GetJob(jobID)
}

func (m *Manager) UpdateJob(job *models.Job) error {
	return m.updateJobMetadata(job)
}

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

func (m *Manager) ListQueues() ([]string, error) {
	return m.inspector.Queues()
}

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

func (m *Manager) updateJobMetadata(job *models.Job) error {
	job.UpdatedAt = time.Now()
	jobData, err := job.ToJSON()
	if err != nil {
		return err
	}
	return m.RedisClient.StoreJobMetadata(job.ID, jobData)
}