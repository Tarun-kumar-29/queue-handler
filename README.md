Queue Handler 2 - Generalized HTTP Job Processing System
A high-performance, Redis-backed queue processing system built with Go, featuring generic HTTP job processing with configurable completion modes and webhook timeouts.

🚀 Features
Generic HTTP Job Processing: Process any HTTP request, not limited to specific use cases
Dual Completion Modes: Immediate and Webhook-based completion with configurable timeouts
Auto-scaling Workers: Dynamic worker management with auto-shutdown per queue
Flexible Metadata System: Generic job identification and grouping via metadata
Bulk Job Scheduling: Submit hundreds of jobs in a single request
Configurable Timeouts: Per-job webhook timeout configuration with multiple strategies
Redis Optimization: High-load optimized Redis connection pooling
Comprehensive Monitoring: Built-in Asynq dashboard integration
Retry Logic: Intelligent retry mechanisms with exponential backoff
RESTful API: Easy integration with HTTP endpoints
Real-time Tracking: Live job status monitoring and webhook confirmations
Backward Compatibility: Supports legacy call-specific jobs
📋 Table of Contents
Architecture
Installation
Configuration
Quick Start
Job Types
API Endpoints
Bulk Job Scheduling
Webhook Timeouts
Monitoring
Testing
Troubleshooting
🏗️ Architecture
Job Processing Modes
Immediate Mode (completion_mode: "immediate")
Jobs complete immediately after HTTP response
Perfect for fast APIs and synchronous operations
High throughput, minimal latency
Webhook Mode (completion_mode: "webhook")
Jobs wait for external webhook confirmation
Configurable timeout (30s to hours)
Multiple timeout strategies: fail, retry, complete
Ideal for async operations requiring confirmation
Auto-detect Mode (default)
Automatically chooses mode based on webhook_url presence
Immediate if no webhook URL, webhook if URL provided
Generic Job Processing
┌─────────────────┐    ┌──────────────┐    ┌─────────────┐
│   HTTP Client   │───▶│ Queue Handler│───▶│   Redis     │
└─────────────────┘    └──────────────┘    └─────────────┘
                              │                     │
                              ▼                     ▼
                       ┌──────────────┐    ┌─────────────┐
                       │   Workers    │    │  Asynq Mon  │
                       └──────────────┘    └─────────────┘
                              │
                              ▼
                       ┌──────────────┐
                       │  Any HTTP API│
                       └──────────────┘
📦 Installation
Prerequisites
Go 1.21 or higher
Redis 6.0 or higher
Docker (optional, for monitoring)
Setup
Clone the repository:
bash
git clone <repository-url>
cd queue-handler-2
Install dependencies:
bash
go mod tidy
Start Redis:
bash
# Option 1: Local Redis
redis-server

# Option 2: Docker Redis
docker run --name redis -d -p 6379:6379 redis:alpine
Build and run:
bash
go run main.go
⚙️ Configuration
Environment Variables
bash
# Redis Configuration
REDIS_HOST=127.0.0.1
REDIS_PORT=6379
REDIS_POOL_SIZE=50
REDIS_READ_TIMEOUT_SECONDS=60
REDIS_WRITE_TIMEOUT_SECONDS=60

# Server Configuration
SERVER_PORT=3000
HTTP_TIMEOUT_SECONDS=300

# Queue Configuration
DEFAULT_CONCURRENCY=5
MAX_RETRIES=3
BACKOFF_DELAY_MS=5000
WEBHOOK_TIMEOUT_SECONDS=600  # 10 minutes default

# Job Configuration
JOB_TTL_HOURS=24
🚀 Quick Start
1. Start the Queue Handler
bash
cd queue-handler-2
go run main.go
2. Start the Test Server (for testing)
bash
cd mimic-server
node server.js
3. Start Monitoring Dashboard
bash
# Option 1: Docker (recommended)
docker run --rm --name asynqmon -p 8081:8080 --link redis:redis hibiken/asynqmon --redis-addr=redis:6379

# Option 2: Local installation
go install github.com/hibiken/asynqmon/cmd/asynqmon@latest
asynqmon --redis-addr=127.0.0.1:6379 --port=8081
Then open: http://localhost:8081

🎯 Job Types
1. Generic HTTP Jobs
Process any HTTP API request with flexible configuration:

bash
# Generic API call with immediate completion
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "api-call-123",
    "url": "https://httpbin.org/post",
    "method": "POST",
    "headers": {
      "Authorization": "Bearer your-token",
      "Content-Type": "application/json"
    },
    "body": "{\"message\": \"Hello from queue\", \"timestamp\": \"2024-01-01T00:00:00Z\"}",
    "queue_name": "api_calls",
    "completion_mode": "immediate",
    "concurrency": 3,
    "metadata": {
      "user_id": "user123",
      "job_type": "api_request",
      "priority": "normal"
    }
  }'
2. Webhook-Based Jobs
Jobs that wait for external confirmation:

bash
# Webhook job with timeout configuration
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "webhook-job-456",
    "url": "https://external-service.com/process",
    "method": "POST",
    "headers": {
      "Content-Type": "application/json",
      "X-API-Key": "your-api-key"
    },
    "body": "{\"data\": \"process this\"}",
    "queue_name": "external_services",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "concurrency": 2,
    "metadata": {
      "service_name": "external_processor",
      "webhook_timeout": "5m",
      "webhook_timeout_strategy": "retry"
    }
  }'
3. Legacy Call Jobs (Backward Compatibility)
Still supports call-specific jobs:

bash
# Legacy call job format
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agent123",
    "prospect_id": "prospect456", 
    "call_id": "call789",
    "url": "http://localhost:4000/initiate-call",
    "method": "POST",
    "headers": {
      "Content-Type": "application/json"
    },
    "body": "{\"prospectId\": \"prospect456\", \"agentId\": \"agent123\"}",
    "queue_name": "calls",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "concurrency": 3
  }'
🔗 API Endpoints
Schedule Single Job
Endpoint: POST /api/schedule-job

Response:

json
{
  "success": true,
  "job_id": "job_abc123",
  "request_id": "api-call-123",
  "call_id": "api-call-123",
  "task_id": "task_def456",
  "message": "Job scheduled in queue 'api_calls' with 3 workers (completion: immediate)"
}
Get Job Status
bash
# By job ID
curl http://localhost:3000/api/job/{job_id}

# By request ID
curl http://localhost:3000/api/job/by-request-id/{request_id}

# By call ID (backward compatibility)
curl http://localhost:3000/api/job/by-call-id/{call_id}
Response:

json
{
  "id": "job_abc123",
  "request_id": "api-call-123",
  "url": "https://httpbin.org/post",
  "method": "POST",
  "status": "completed",
  "queue_name": "api_calls",
  "completion_mode": "immediate",
  "created_at": "2025-06-02T10:30:00Z",
  "completed_at": "2025-06-02T10:30:05Z",
  "metadata": {
    "user_id": "user123",
    "job_type": "api_request"
  },
  "response": {
    "status_code": 200,
    "duration": "1.234s"
  }
}
Queue Management
bash
# List all queues
curl http://localhost:3000/api/queues

# Get active queues with details
curl http://localhost:3000/api/queues/active

# Get queue statistics
curl http://localhost:3000/api/queue/stats?queue=api_calls

# Scale queue workers
curl -X POST "http://localhost:3000/api/queue/scale?queue=api_calls&workers=5"

# Start queue manually
curl -X POST http://localhost:3000/api/queue/start/api_calls?concurrency=3

# Stop queue
curl -X POST http://localhost:3000/api/queue/stop/api_calls
Worker Management
bash
# Get worker statistics
curl http://localhost:3000/api/workers/stats

# Get detailed worker info
curl http://localhost:3000/api/workers/active
Webhook Endpoint
Endpoint: POST /api/webhook

External services send confirmations here:

bash
# Webhook confirmation
curl -X POST http://localhost:3000/api/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "webhook-job-456",
    "status": "completed",
    "response": {
      "external_id": "ext-12345",
      "result": "success"
    }
  }'
📦 Bulk Job Scheduling
Simple Bulk Jobs
bash
# 100 immediate jobs
curl -X POST http://localhost:3000/api/schedule-bulk-jobs \
  -H "Content-Type: application/json" \
  -d '{
    "default_method": "POST",
    "default_concurrency": 3,
    "default_headers": {
      "Content-Type": "application/json"
    },
    "jobs": [
      {
        "request_id": "bulk-1",
        "url": "https://httpbin.org/post",
        "body": "{\"bulk_job\": 1}",
        "queue_name": "bulk_immediate",
        "completion_mode": "immediate",
        "metadata": {"batch": "test_1"}
      },
      {
        "request_id": "bulk-2", 
        "url": "https://httpbin.org/post",
        "body": "{\"bulk_job\": 2}",
        "queue_name": "bulk_immediate",
        "completion_mode": "immediate",
        "metadata": {"batch": "test_1"}
      }
    ]
  }'
Mixed Bulk Jobs
bash
# Mixed immediate and webhook jobs
curl -X POST http://localhost:3000/api/schedule-bulk-jobs \
  -H "Content-Type: application/json" \
  -d '{
    "default_method": "POST",
    "default_max_retries": 3,
    "default_headers": {
      "Content-Type": "application/json"
    },
    "jobs": [
      {
        "request_id": "mixed-immediate-1",
        "url": "https://httpbin.org/post", 
        "body": "{\"type\": \"immediate\"}",
        "queue_name": "mixed_immediate",
        "completion_mode": "immediate",
        "concurrency": 4,
        "metadata": {"type": "immediate"}
      },
      {
        "request_id": "mixed-webhook-1",
        "url": "http://localhost:4000/webhook-api",
        "body": "{\"type\": \"webhook\"}",
        "queue_name": "mixed_webhook", 
        "completion_mode": "webhook",
        "webhook_url": "http://localhost:3000/api/webhook",
        "concurrency": 2,
        "metadata": {"type": "webhook"}
      },
      {
        "agent_id": "agent123",
        "prospect_id": "prospect456",
        "url": "http://localhost:4000/initiate-call",
        "body": "{\"legacy\": true}",
        "completion_mode": "webhook",
        "webhook_url": "http://localhost:3000/api/webhook"
      }
    ]
  }'
Response:

json
{
  "success": true,
  "total_jobs": 3,
  "successful": 3,
  "failed": 0,
  "results": [
    {
      "success": true,
      "job_id": "job_123",
      "request_id": "mixed-immediate-1",
      "queue_name": "mixed_immediate",
      "index": 0
    }
  ],
  "processing_time": "45ms",
  "queue_summary": {
    "mixed_immediate": 1,
    "mixed_webhook": 1,
    "agent_agent123": 1
  }
}
⏰ Webhook Timeouts
Timeout Configuration
Configure webhook timeouts per job:

bash
# Job with custom timeout
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "timeout-test",
    "url": "http://localhost:4000/webhook-api",
    "method": "POST",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "metadata": {
      "webhook_timeout": "5m",
      "webhook_timeout_strategy": "retry"
    }
  }'
Timeout Strategies
Available strategies:

"fail" (default): Mark job as failed when timeout occurs
"retry": Retry job if timeout occurs (if retries available)
"complete": Mark job as completed despite timeout
Timeout Formats
json
{
  "metadata": {
    "webhook_timeout_seconds": 300,      // 5 minutes
    "webhook_timeout_minutes": 10,       // 10 minutes
    "webhook_timeout": "30m",           // 30 minutes (duration string)
    "webhook_timeout_strategy": "retry"
  }
}
Timeout Priority Order
webhook_timeout_seconds (job metadata)
webhook_timeout_minutes (job metadata)
webhook_timeout (job metadata, duration string)
Server config WEBHOOK_TIMEOUT_SECONDS
10 minutes (default)
📊 Monitoring
Queue Statistics
bash
# System overview
curl http://localhost:3000/api/queues

# Active queues
curl http://localhost:3000/api/queues/active

# Specific queue stats
curl http://localhost:3000/api/queue/stats?queue=api_calls
Response:

json
{
  "queue_name": "api_calls",
  "pending_jobs": 5,
  "active_jobs": 3,
  "completed_jobs": 157,
  "failed_jobs": 2,
  "workers_active": 4
}
Asynq Dashboard
Access the monitoring dashboard at http://localhost:8081

Features:

📈 Real-time queue statistics
📋 Job listing and details
🔄 Active/pending/completed job counts
❌ Failed job analysis
📊 Performance metrics
🕐 Historical data
🧪 Testing
Basic HTTP Job Test
bash
# Test immediate HTTP job
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "test-'$(date +%s)'",
    "url": "https://httpbin.org/delay/2",
    "method": "GET",
    "queue_name": "test_immediate",
    "completion_mode": "immediate",
    "metadata": {"test": true}
  }'

# Test webhook HTTP job  
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "webhook-test-'$(date +%s)'",
    "url": "http://localhost:4000/webhook-api",
    "method": "POST",
    "body": "{\"test\": true}",
    "queue_name": "test_webhook",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "metadata": {"test": true}
  }'
Bulk Job Test
bash
# Test bulk job scheduling
node bulk-test.js
The test script schedules 350+ jobs across multiple queues:

100 immediate jobs across 5 queues
100 webhook jobs across 4 queues
150 mixed jobs across multiple queues
Timeout Test
bash
# Test webhook timeout
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "timeout-test-'$(date +%s)'",
    "url": "http://localhost:4000/webhook-api",
    "method": "POST",
    "body": "{\"delay_seconds\": 60}",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "metadata": {
      "webhook_timeout": "30s",
      "webhook_timeout_strategy": "fail"
    }
  }'
Health Checks
bash
# Application health
curl http://localhost:3000/

# Queue health
curl http://localhost:3000/api/queues/active

# Test server health
curl http://localhost:4000/health
🔧 Troubleshooting
Common Issues
1. Jobs Stuck in Processing
Check:

bash
# Check worker status
curl http://localhost:3000/api/workers/stats

# Check specific queue
curl http://localhost:3000/api/queue/stats?queue=stuck_queue

# Check Asynq dashboard
open http://localhost:8081
2. Webhook Timeouts
Check job with timeout:

bash
curl http://localhost:3000/api/job/by-request-id/timeout-job-id
Manual webhook confirmation:

bash
curl -X POST http://localhost:3000/api/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "stuck-job-id",
    "status": "completed"
  }'
3. Queue Not Auto-Scaling
Check active queues:

bash
curl http://localhost:3000/api/queues/active
Manually start queue:

bash
curl -X POST http://localhost:3000/api/queue/start/queue_name?concurrency=5
4. High Redis Memory Usage
Check Redis info:

bash
redis-cli INFO memory
redis-cli KEYS "asynq:*" | wc -l
Clear completed jobs:

bash
curl -X POST http://localhost:3000/api/queue/clear \
  -H "Content-Type: application/json" \
  -d '{"queue_name": "completed_queue"}'
Debug Commands
bash
# Check Redis connection
redis-cli ping

# Monitor Redis operations
redis-cli MONITOR

# Check all queues
redis-cli KEYS "asynq:queues:*"

# Check job details
redis-cli HGETALL "asynq:job:job_id"
📈 Production Tips
Performance Optimization
Queue Distribution: Spread jobs across multiple queues
Worker Tuning: Adjust concurrency based on job type
Timeout Configuration: Set appropriate timeouts per job type
Bulk Processing: Use bulk endpoints for high-volume scenarios
Monitoring Setup
bash
# Set up alerts for failed jobs
curl http://localhost:3000/api/queue/stats | jq '.failed_jobs'

# Monitor worker scaling
curl http://localhost:3000/api/workers/stats | jq '.total_workers'

# Track job processing rates
curl http://localhost:3000/api/queues/active | jq '.active_queues'
Scaling Recommendations
Redis: Use Redis Cluster for high availability
Workers: Scale based on job processing time and volume
Timeouts: Configure based on external service SLAs
Monitoring: Set up alerting for queue depth and failure rates
🎯 Key Capabilities
✅ Generic HTTP Processing: Queue any HTTP request to any API
✅ Flexible Job Configuration: Metadata-driven job customization
✅ Auto-Scaling Workers: Dedicated workers per queue with auto-shutdown
✅ Configurable Timeouts: Per-job webhook timeout control
✅ Bulk Job Submission: Handle hundreds of jobs in single requests
✅ Backward Compatibility: Legacy call jobs still supported
✅ Real-Time Monitoring: Live dashboard and REST API monitoring
✅ High Performance: Optimized for enterprise-scale workloads

Perfect for: API orchestration, async job processing, webhook management, microservice communication, and any HTTP-based workflow automation.

Happy Queue Processing! 🚀

