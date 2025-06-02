package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"queue-handler-2/internal/queue"
	"queue-handler-2/pkg/redis"
)

type HealthHandler struct {
	startTime    time.Time
	queueManager *queue.Manager
	redisClient  *redis.Client
}

type HealthResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Uptime    string                 `json:"uptime"`
	Version   string                 `json:"version"`
	Backend   string                 `json:"backend"`
	System    map[string]interface{} `json:"system"`
	Services  map[string]interface{} `json:"services"`
}

func NewHealthHandler(queueManager *queue.Manager, redisClient *redis.Client) *HealthHandler {
	return &HealthHandler{
		startTime:    time.Now(),
		queueManager: queueManager,
		redisClient:  redisClient,
	}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.startTime)

	// Get system info
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	systemInfo := map[string]interface{}{
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc":    formatBytes(memStats.Alloc),
		"memory_total":    formatBytes(memStats.TotalAlloc),
		"memory_sys":      formatBytes(memStats.Sys),
		"gc_runs":         memStats.NumGC,
		"go_version":      runtime.Version(),
		"go_os":           runtime.GOOS,
		"go_arch":         runtime.GOARCH,
		"cpu_count":       runtime.NumCPU(),
	}

	// Check service health
	services := h.checkServiceHealth()

	// Determine overall status
	status := "healthy"
	for _, serviceStatus := range services {
		if serviceMap, ok := serviceStatus.(map[string]interface{}); ok {
			if serviceMap["status"] != "healthy" {
				status = "unhealthy"
				break
			}
		}
	}

	response := HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Uptime:    uptime.String(),
		Version:   "1.0.0",
		Backend:   "asynq",
		System:    systemInfo,
		Services:  services,
	}

	statusCode := http.StatusOK
	if status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// checkServiceHealth checks the health of dependent services
func (h *HealthHandler) checkServiceHealth() map[string]interface{} {
	services := make(map[string]interface{})

	// Check Redis health
	redisStatus := "healthy"
	redisError := ""
	if err := h.redisClient.Ping(); err != nil {
		redisStatus = "unhealthy"
		redisError = err.Error()
	}

	services["redis"] = map[string]interface{}{
		"status": redisStatus,
		"error":  redisError,
	}

	// Check queue system health (basic queue stats)
	queueStatus := "healthy"
	queueError := ""
	queueStats := make(map[string]interface{})

	if stats, err := h.queueManager.GetQueueStats("default"); err != nil {
		queueStatus = "unhealthy"
		queueError = err.Error()
	} else {
		queueStats = map[string]interface{}{
			"pending_jobs":   stats.PendingJobs,
			"active_jobs":    stats.ActiveJobs,
			"completed_jobs": stats.CompletedJobs,
			"failed_jobs":    stats.FailedJobs,
		}
	}

	services["queue"] = map[string]interface{}{
		"status": queueStatus,
		"error":  queueError,
		"stats":  queueStats,
	}

	// Check webhook pending jobs
	webhookStatus := "healthy"
	webhookError := ""
	webhookStats := make(map[string]interface{})

	if pendingJobs, err := h.queueManager.GetWebhookPendingJobs(); err != nil {
		webhookStatus = "degraded"
		webhookError = err.Error()
	} else {
		webhookStats = map[string]interface{}{
			"pending_webhooks": len(pendingJobs),
		}
	}

	services["webhooks"] = map[string]interface{}{
		"status": webhookStatus,
		"error":  webhookError,
		"stats":  webhookStats,
	}

	return services
}

// DetailedHealth provides more detailed health information
func (h *HealthHandler) DetailedHealth(w http.ResponseWriter, r *http.Request) {
	response := make(map[string]interface{})

	// Basic health info
	uptime := time.Since(h.startTime)
	response["uptime"] = uptime.String()
	response["timestamp"] = time.Now().Format(time.RFC3339)
	response["backend"] = "asynq"

	// Queue statistics for all queues
	queues := []string{"critical", "default", "low"}
	queueStats := make(map[string]interface{})

	for _, queueName := range queues {
		if stats, err := h.queueManager.GetQueueStats(queueName); err == nil {
			queueStats[queueName] = stats
		}
	}
	response["queue_stats"] = queueStats

	// System resources
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	response["system"] = map[string]interface{}{
		"goroutines":         runtime.NumGoroutine(),
		"memory_alloc_mb":    bToMb(memStats.Alloc),
		"memory_total_mb":    bToMb(memStats.TotalAlloc),
		"memory_sys_mb":      bToMb(memStats.Sys),
		"gc_runs":            memStats.NumGC,
		"gc_pause_total_ns":  memStats.PauseTotalNs,
		"heap_objects":       memStats.HeapObjects,
		"next_gc_mb":         bToMb(memStats.NextGC),
	}

	// Service connectivity
	response["services"] = h.checkServiceHealth()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// ReadinessProbe for Kubernetes readiness checks
func (h *HealthHandler) ReadinessProbe(w http.ResponseWriter, r *http.Request) {
	// Check if essential services are available
	if err := h.redisClient.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Redis unavailable"))
		return
	}

	// Check if we can get basic queue stats
	if _, err := h.queueManager.GetQueueStats("default"); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Queue manager unavailable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Ready"))
}

// LivenessProbe for Kubernetes liveness checks
func (h *HealthHandler) LivenessProbe(w http.ResponseWriter, r *http.Request) {
	// Simple liveness check - just verify the application is running
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Alive"))
}

// Helper functions

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}