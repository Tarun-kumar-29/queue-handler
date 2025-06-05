package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"queue-handler-2/internal/config"
	"queue-handler-2/internal/handlers"
	"queue-handler-2/internal/middleware"
	"queue-handler-2/internal/queue"
	"queue-handler-2/internal/worker"
	"queue-handler-2/pkg/redis"

	"github.com/gorilla/mux"
)

func main() {
	log.Println("🔧 Starting Queue Handler Server with Asynq...")

	// Load configuration
	cfg := config.LoadConfig()

	log.Printf("📋 Config: Redis=%s, Port=%d, Workers=%d",
		cfg.GetRedisAddr(), cfg.ServerPort, cfg.DefaultConcurrency)

	// Initialize Redis client
	redisClient := redis.NewClient(cfg)
	defer redisClient.Close()

	// Initialize queue manager (Asynq-based)
	queueManager := queue.NewManager(redisClient, cfg)
	defer queueManager.Close()

	// Initialize worker manager (now supports dedicated workers per queue)
	workerManager := worker.NewManager(queueManager, cfg)
	defer workerManager.StopAllWorkers()

	// Workers will start automatically when jobs are enqueued
	log.Println("🎯 Enhanced worker manager ready - dedicated workers will start on demand")

	// Initialize handlers
	jobHandler := handlers.NewJobHandler(queueManager, workerManager, cfg)
	webhookHandler := handlers.NewWebhookHandler(queueManager)
	healthHandler := handlers.NewHealthHandler(queueManager, redisClient)

	// Setup routes
	router := mux.NewRouter()

	// Apply middleware
	router.Use(middleware.LoggingMiddleware)
	router.Use(middleware.CORSMiddleware)
	router.Use(middleware.RecoveryMiddleware)
	router.Use(middleware.RequestIDMiddleware)

	// API routes - same structure as before
	api := router.PathPrefix("/api").Subrouter()

	// Job management
	api.HandleFunc("/schedule-job", jobHandler.ScheduleJob).Methods("POST")
	api.HandleFunc("/job", jobHandler.GetJob).Methods("GET")
	api.HandleFunc("/job/{id}", jobHandler.GetJob).Methods("GET")
	api.HandleFunc("/job/{id}", jobHandler.DeleteJob).Methods("DELETE")
	api.HandleFunc("/job/{id}/archive", jobHandler.ArchiveJob).Methods("POST")
	api.HandleFunc("/jobs/call/{call_id}", jobHandler.GetJobByCallID).Methods("GET")
	api.HandleFunc("/schedule-bulk-jobs", jobHandler.ScheduleBulkJobs).Methods("POST")

	// Queue management - Enhanced with dedicated worker support
	api.HandleFunc("/queue/stats", jobHandler.GetQueueStats).Methods("GET")
	api.HandleFunc("/queue/scale", jobHandler.ScaleQueue).Methods("POST")
	api.HandleFunc("/queue/{queue}/pause", jobHandler.PauseQueue).Methods("POST")
	api.HandleFunc("/queue/{queue}/unpause", jobHandler.UnpauseQueue).Methods("POST")
	api.HandleFunc("/queue/{queue}/start", jobHandler.ManualStartQueue).Methods("POST")
	api.HandleFunc("/queue/{queue}/stop", jobHandler.ManualStopQueue).Methods("POST")
	api.HandleFunc("/queues", jobHandler.ListQueues).Methods("GET")
	api.HandleFunc("/queues/active", jobHandler.GetActiveQueuesDetailed).Methods("GET")
	api.HandleFunc("/queue/clear", jobHandler.ClearQueue).Methods("POST")

	// Worker management
	api.HandleFunc("/workers/stats", jobHandler.GetWorkerStats).Methods("GET")

	// Webhook management
	api.HandleFunc("/webhook", webhookHandler.HandleWebhook).Methods("POST")
	api.HandleFunc("/webhook/pending", webhookHandler.GetWebhookPendingJobs).Methods("GET")

	// Health and monitoring endpoints
	router.HandleFunc("/health", healthHandler.Health).Methods("GET")
	router.HandleFunc("/health/detailed", healthHandler.DetailedHealth).Methods("GET")
	router.HandleFunc("/health/ready", healthHandler.ReadinessProbe).Methods("GET")
	router.HandleFunc("/health/live", healthHandler.LivenessProbe).Methods("GET")

	// Root endpoint
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]interface{}{
			"service":   "queue-handler",
			"version":   "1.0.0",
			"backend":   "asynq",
			"status":    "running",
			"timestamp": time.Now().Format(time.RFC3339),
			"docs":      "See /api endpoints for usage",
		}
		json.NewEncoder(w).Encode(response)
	}).Methods("GET")

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("🚀 Server starting on port %d", cfg.ServerPort)
		log.Println("📚 Available endpoints:")
		log.Println("  GET  / - Service information")
		log.Println("  GET  /health - Health check")
		log.Println("  GET  /health/detailed - Detailed health info")
		log.Println("  GET  /health/ready - Readiness probe")
		log.Println("  GET  /health/live - Liveness probe")
		log.Println("  POST /api/schedule-job - Schedule job")
		log.Println("  GET  /api/job/{id} - Get job by ID")
		log.Println("  GET  /api/jobs/call/{call_id} - Get job by CallID")
		log.Println("  GET  /api/queue/stats?queue=<name> - Queue statistics")
		log.Println("  GET  /api/queues - List all queues")
		log.Println("  GET  /api/workers/stats - Worker statistics")
		log.Println("  POST /api/webhook - Process webhooks")
		log.Println("  GET  /api/webhook/pending - Pending webhook jobs")
		log.Println("")
		log.Println("🎛️ Asynq Web UI: Install asynqmon and run:")
		log.Printf("     asynq dash --redis-addr=%s", cfg.GetRedisAddr())
		log.Println("     Then open http://localhost:8080")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down server...")

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// Shutdown server gracefully
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("❌ Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server exited gracefully")
}
