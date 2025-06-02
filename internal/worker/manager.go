package worker

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/queue"
	"queue-handler-2/pkg/redis"

	"github.com/hibiken/asynq"
)

// QueueWorkers manages dedicated workers for a specific queue
type QueueWorkers struct {
	QueueName   string
	Server      *asynq.Server
	Mux         *asynq.ServeMux
	Worker      *Worker
	Concurrency int
	IsRunning   bool
}

// Manager manages multiple dedicated worker instances, one per queue
type Manager struct {
	config       *config.Config
	RedisClient  *redis.Client
	queueManager *queue.Manager
	
	// Map of queue name -> dedicated workers
	queueWorkers map[string]*QueueWorkers
	workerMutex  sync.RWMutex
}

// NewManager creates a new enhanced worker manager supporting dedicated workers per queue
func NewManager(queueManager *queue.Manager, config *config.Config) *Manager {
	manager := &Manager{
		config:       config,
		RedisClient:  queueManager.RedisClient,
		queueManager: queueManager,
		queueWorkers: make(map[string]*QueueWorkers),
	}

	// Set callbacks for queue lifecycle management
	queueManager.SetWorkerCallbacks(manager.startQueueWorkers, manager.stopQueueWorkers)

	log.Println("✅ Enhanced Worker Manager initialized with dedicated worker support")
	return manager
}

// startQueueWorkers starts dedicated workers for a specific queue
func (m *Manager) startQueueWorkers(queueName string, concurrency int) error {
	m.workerMutex.Lock()
	defer m.workerMutex.Unlock()

	// Stop existing workers if any
	if existing, exists := m.queueWorkers[queueName]; exists && existing.IsRunning {
		log.Printf("🔄 Stopping existing workers for queue %s", queueName)
		existing.Server.Stop()
		existing.IsRunning = false
	}

	log.Printf("🚀 Starting %d dedicated workers for queue: %s", concurrency, queueName)

	// Create Asynq server configuration for this specific queue
	asynqConfig := asynq.Config{
		Concurrency: concurrency,
		
		// CRITICAL FIX: Only process this specific queue
		Queues: map[string]int{
			queueName: 10, // High priority for this queue
			// Do NOT include other queues - this server is dedicated to one queue only
		},
		
		// ADDITIONAL FIX: Add queue filter to be extra sure
		IsFailure: func(err error) bool {
			// Custom failure detection if needed
			return true
		},
		
		// Retry configuration
		RetryDelayFunc: func(n int, err error, task *asynq.Task) time.Duration {
			multiplier := 1 << uint(n) // 2^n exponential backoff
			delay := time.Duration(multiplier) * m.config.BackoffDelay
			
			maxDelay := 10 * time.Minute
			if delay > maxDelay {
				delay = maxDelay
			}
			return delay
		},
		
		// Error handler
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
			log.Printf("❌ Queue %s task error: Type=%s, Error=%v", queueName, task.Type(), err)
		}),
		
		// Shutdown timeout
		ShutdownTimeout: m.config.ShutdownTimeout,
		
		// Health check
		HealthCheckFunc: func(err error) {
			if err != nil {
				log.Printf("⚠️ Queue %s health check failed: %v", queueName, err)
			}
		},
		
		// Removed logger config - not available in this Asynq version
	}

	// Create dedicated Redis options for this queue worker
	redisOpts := m.RedisClient.GetAsynqOptions()
	
	// Create dedicated Asynq server for this queue
	server := asynq.NewServer(redisOpts, asynqConfig)

	log.Printf("🔍 DEBUG: Created server for queue %s with concurrency %d", queueName, concurrency)
log.Printf("🔍 DEBUG: Current queue workers count: %d", len(m.queueWorkers))

// List all active workers
for qName, qw := range m.queueWorkers {
    if qw.IsRunning {
        log.Printf("🔍 DEBUG: Active queue: %s, concurrency: %d", qName, qw.Concurrency)
    }
}
	
	// Create dedicated worker instance
	worker := NewWorker(m.queueManager, m.config)
	
	// Create mux and register handlers
	mux := asynq.NewServeMux()
	
	// Add middleware to ensure we only process jobs from our queue
	mux.Use(func(h asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			// Get the queue name from the task context
			taskQueue, ok := asynq.GetQueueName(ctx)
			if !ok || taskQueue != queueName {
				log.Printf("⚠️ Queue %s worker received task from wrong queue: %s", queueName, taskQueue)
				return fmt.Errorf("wrong queue: expected %s, got %s", queueName, taskQueue)
			}
			
			log.Printf("✅ Queue %s worker processing task from correct queue", queueName)
			return h.ProcessTask(ctx, task)
		})
	})
	
	mux.HandleFunc(queue.TaskTypeHTTPRequest, worker.ProcessHTTPRequest)

	// Create queue workers struct
	queueWorkers := &QueueWorkers{
		QueueName:   queueName,
		Server:      server,
		Mux:         mux,
		Worker:      worker,
		Concurrency: concurrency,
		IsRunning:   false,
	}

	// Start server in goroutine
	go func() {
		log.Printf("👷 Queue %s workers starting with %d concurrency (DEDICATED)", queueName, concurrency)
		if err := server.Run(mux); err != nil {
			log.Printf("❌ Queue %s workers failed: %v", queueName, err)
			m.workerMutex.Lock()
			if qw, exists := m.queueWorkers[queueName]; exists {
				qw.IsRunning = false
			}
			m.workerMutex.Unlock()
		}
	}()

	queueWorkers.IsRunning = true
	m.queueWorkers[queueName] = queueWorkers

	log.Printf("✅ Dedicated workers started: Queue=%s, Workers=%d, Concurrency=%d", 
		queueName, concurrency, concurrency)
	return nil
}

// stopQueueWorkers stops dedicated workers for a specific queue
func (m *Manager) stopQueueWorkers(queueName string, _ int) error {
	m.workerMutex.Lock()
	defer m.workerMutex.Unlock()

	queueWorkers, exists := m.queueWorkers[queueName]
	if !exists || !queueWorkers.IsRunning {
		return nil // Already stopped or doesn't exist
	}

	log.Printf("🛑 Stopping dedicated workers for queue: %s", queueName)

	// Graceful shutdown
	queueWorkers.Server.Stop()
	queueWorkers.IsRunning = false

	// Keep the struct but mark as not running for potential restart
	log.Printf("✅ Workers stopped for queue: %s", queueName)
	return nil
}

// StartWorkers starts workers for a queue (maintains compatibility with original interface)
func (m *Manager) StartWorkers(queueName string, concurrency int) error {
	return m.startQueueWorkers(queueName, concurrency)
}

// StopWorkers stops workers for a specific queue
func (m *Manager) StopWorkers(queueName string) error {
	return m.stopQueueWorkers(queueName, 0)
}

// StopAllWorkers stops all dedicated workers
func (m *Manager) StopAllWorkers() {
	m.workerMutex.Lock()
	defer m.workerMutex.Unlock()

	log.Println("🛑 Stopping all dedicated workers...")

	for queueName, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			log.Printf("🛑 Stopping workers for queue: %s", queueName)
			queueWorkers.Server.Shutdown() // Force shutdown
			queueWorkers.IsRunning = false
		}
	}

	log.Println("✅ All dedicated workers stopped")
}

// GetWorkerCount returns the number of active workers for a specific queue
func (m *Manager) GetWorkerCount(queueName string) int {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()

	if queueWorkers, exists := m.queueWorkers[queueName]; exists && queueWorkers.IsRunning {
		return queueWorkers.Concurrency
	}
	return 0
}

// GetTotalWorkerCount returns the total number of active workers across all queues
func (m *Manager) GetTotalWorkerCount() int {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()

	total := 0
	for _, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			total += queueWorkers.Concurrency
		}
	}
	return total
}

// ListQueues returns all queues with active workers
func (m *Manager) ListQueues() []string {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()

	queues := make([]string, 0, len(m.queueWorkers))
	for queueName, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			queues = append(queues, queueName)
		}
	}
	return queues
}

// ScaleWorkers changes the concurrency for a specific queue
func (m *Manager) ScaleWorkers(queueName string, newConcurrency int) error {
	if newConcurrency <= 0 {
		// Stop workers for this queue
		return m.StopWorkers(queueName)
	}

	// Restart with new concurrency
	log.Printf("📈 Scaling queue %s to %d workers", queueName, newConcurrency)
	
	// Stop current workers
	if err := m.stopQueueWorkers(queueName, 0); err != nil {
		return fmt.Errorf("failed to stop current workers: %w", err)
	}

	// Wait a moment for graceful shutdown
	time.Sleep(1 * time.Second)

	// Start with new concurrency
	return m.startQueueWorkers(queueName, newConcurrency)
}

// GetWorkerStats returns detailed worker statistics
func (m *Manager) GetWorkerStats() map[string]interface{} {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()

	queueDetails := make(map[string]map[string]interface{})
	totalWorkers := 0
	activeQueues := 0

	for queueName, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			queueDetails[queueName] = map[string]interface{}{
				"concurrency":  queueWorkers.Concurrency,
				"is_running":   queueWorkers.IsRunning,
				"queue_name":   queueName,
			}
			totalWorkers += queueWorkers.Concurrency
			activeQueues++
		}
	}

	return map[string]interface{}{
		"total_workers":    totalWorkers,
		"active_queues":    activeQueues,
		"backend":          "asynq-dedicated",
		"queue_details":    queueDetails,
		"worker_model":     "dedicated-per-queue",
		"auto_shutdown":    true,
		"shutdown_timeout": m.config.ShutdownTimeout.String(),
	}
}

// IsRunning returns whether any workers are currently running
func (m *Manager) IsRunning() bool {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()

	for _, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			return true
		}
	}
	return false
}

// GetActiveJobs returns information about currently processing jobs across all queues
func (m *Manager) GetActiveJobs() (map[string]interface{}, error) {
	inspector := asynq.NewInspector(m.RedisClient.GetAsynqOptions())
	defer inspector.Close()

	result := make(map[string]interface{})
	
	m.workerMutex.RLock()
	activeQueues := make([]string, 0, len(m.queueWorkers))
	for queueName, queueWorkers := range m.queueWorkers {
		if queueWorkers.IsRunning {
			activeQueues = append(activeQueues, queueName)
		}
	}
	m.workerMutex.RUnlock()

	for _, queueName := range activeQueues {
		queueInfo, err := inspector.GetQueueInfo(queueName)
		if err != nil {
			continue
		}
		
		result[queueName] = map[string]interface{}{
			"active":    queueInfo.Active,
			"pending":   queueInfo.Pending,
			"scheduled": queueInfo.Scheduled,
			"retry":     queueInfo.Retry,
			"workers":   m.GetWorkerCount(queueName),
		}
	}

	return result, nil
}

// GetQueueWorkerInfo returns detailed information about workers for a specific queue
func (m *Manager) GetQueueWorkerInfo(queueName string) (*QueueWorkers, bool) {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()
	
	queueWorkers, exists := m.queueWorkers[queueName]
	return queueWorkers, exists
}

// RestartQueue restarts workers for a specific queue with optional new concurrency
func (m *Manager) RestartQueue(queueName string, concurrency int) error {
	if concurrency == 0 {
		concurrency = m.config.DefaultConcurrency
	}

	log.Printf("🔄 Restarting queue %s with %d workers", queueName, concurrency)
	
	// Stop current workers
	if err := m.stopQueueWorkers(queueName, 0); err != nil {
		log.Printf("⚠️ Error stopping queue %s: %v", queueName, err)
	}

	// Wait for graceful shutdown
	time.Sleep(2 * time.Second)

	// Start with new/same concurrency
	return m.startQueueWorkers(queueName, concurrency)
}

// GetServer returns the Asynq server for a specific queue (for advanced usage)
func (m *Manager) GetServer(queueName string) (*asynq.Server, bool) {
	m.workerMutex.RLock()
	defer m.workerMutex.RUnlock()
	
	if queueWorkers, exists := m.queueWorkers[queueName]; exists {
		return queueWorkers.Server, true
	}
	return nil, false
}