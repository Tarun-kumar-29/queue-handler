# Queue Handler 2 - Advanced Queue Management System

A high-performance, Redis-backed queue processing system built with Go, featuring multiple completion modes for different use cases.

## 🚀 Features

- **Dual Completion Modes**: Immediate and Webhook-based completion
- **Auto-scaling Workers**: Dynamic worker management with auto-shutdown
- **Redis Optimization**: High-load optimized Redis connection pooling
- **Comprehensive Monitoring**: Built-in Asynq dashboard integration
- **Retry Logic**: Intelligent retry mechanisms with exponential backoff
- **RESTful API**: Easy integration with HTTP endpoints
- **Real-time Tracking**: Live job status monitoring and webhook confirmations

## 📋 Table of Contents

- [Architecture](#architecture)
- [Installation](#installation)
- [Configuration](#configuration)
- [Quick Start](#quick-start)
- [Completion Modes](#completion-modes)
- [API Endpoints](#api-endpoints)
- [Monitoring](#monitoring)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)

## 🏗️ Architecture

### Completion Modes

1. **Immediate Mode** (`completion_mode: "immediate"`)
   - Jobs complete immediately after HTTP response
   - Perfect for fast APIs and synchronous operations
   - High throughput, minimal latency

2. **Webhook Mode** (`completion_mode: "webhook"`)
   - Jobs wait for external webhook confirmation
   - Ideal for async operations (calls, long-running tasks)
   - Reliable completion tracking

3. **Auto-detect Mode** (default)
   - Automatically chooses mode based on `webhook_url` presence
   - Immediate if no webhook URL, webhook if URL provided

### Components

```
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
                       │ External APIs│
                       └──────────────┘
```

## 📦 Installation

### Prerequisites

- Go 1.21 or higher
- Redis 6.0 or higher
- Docker (optional, for monitoring)

### Setup

1. **Clone the repository:**
   ```bash
   git clone <repository-url>
   cd queue-handler-2
   ```

2. **Install dependencies:**
   ```bash
   go mod tidy
   ```

3. **Start Redis:**
   ```bash
   # Option 1: Local Redis
   redis-server
   
   # Option 2: Docker Redis
   docker run --name redis -d -p 6379:6379 redis:alpine
   ```

4. **Build and run:**
   ```bash
   go run main.go
   ```

## ⚙️ Configuration

### Environment Variables

```bash
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
WEBHOOK_TIMEOUT_SECONDS=120

# Job Configuration
JOB_TTL_HOURS=24
```

### Redis Optimization

The system includes Redis optimizations for high-load scenarios:
- Connection pooling (50 connections)
- Increased timeouts (60s read/write)
- Retry logic with exponential backoff
- Connection lifecycle management

## 🚀 Quick Start

### 1. Start the Queue Handler

```bash
cd queue-handler-2
go run main.go
```

Output:
```
📋 Configuration Loaded:
   Redis: 127.0.0.1:6379 (pool: 50, timeouts: 30s/60s/60s)
   Server: :3000 (concurrency: 5, retries: 3)
🚀 Server starting on :3000
🎛️ Asynq Web UI: Install asynqmon and run:
     asynq dash --redis-addr=127.0.0.1:6379
     Then open http://localhost:8080
```

### 2. Start the Mock Server (for testing)

```bash
cd mimic-server
node server.js
```

### 3. Start Monitoring Dashboard

```bash
# Option 1: Docker (recommended)
docker run --name redis -d -p 6379:6379 redis:alpine
docker run --rm --name asynqmon -p 8081:8080 --link redis:redis hibiken/asynqmon --redis-addr=redis:6379

# Option 2: Local installation
go install github.com/hibiken/asynqmon/cmd/asynqmon@latest
asynqmon --redis-addr=127.0.0.1:6379 --port=8081
```

Then open: **http://localhost:8081**

## 🎯 Completion Modes

### Immediate Completion

Perfect for fast APIs, data processing, synchronous operations.

**Example Use Cases:**
- REST API calls
- Data transformations
- Quick validations
- File processing

**Characteristics:**
- ✅ Completes immediately after HTTP response
- ✅ High throughput (no waiting)
- ✅ Low latency
- ✅ Simple error handling

### Webhook Completion

Ideal for async operations that need confirmation.

**Example Use Cases:**
- Phone call initiation
- Email sending services
- Long-running data processing
- Third-party integrations

**Characteristics:**
- ⏳ Waits for external confirmation
- 🔄 Reliable completion tracking
- 📞 Real-time status updates
- ⚡ Timeout protection

### Auto-detect Mode

Automatically chooses the appropriate mode.

**Logic:**
- If `webhook_url` is provided → Webhook mode
- If no `webhook_url` → Immediate mode
- Can be overridden with explicit `completion_mode`

## 🔗 API Endpoints

### Schedule Job

**Endpoint:** `POST /api/schedule-job`

#### Immediate Completion Job

```bash
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "immediate-job-001",
    "url": "http://localhost:4000/immediate-api",
    "method": "POST",
    "headers": {
      "Content-Type": "application/json"
    },
    "body": "{\"prospectId\":\"test-001\",\"data\":\"immediate processing\"}",
    "queue_name": "immediate_queue",
    "completion_mode": "immediate",
    "max_retries": 3,
    "concurrency": 3
  }'
```

**Response:**
```json
{
  "success": true,
  "job_id": "job_abc123",
  "call_id": "immediate-job-001",
  "task_id": "task_def456",
  "message": "Job scheduled in queue 'immediate_queue' with 3 workers (completion: immediate)"
}
```

#### Webhook Completion Job

```bash
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "webhook-job-002",
    "url": "http://localhost:4000/initiate-call",
    "method": "POST",
    "headers": {
      "Content-Type": "application/json"
    },
    "body": "{\"prospectId\":\"test-002\",\"data\":\"async processing\"}",
    "queue_name": "webhook_queue",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook",
    "max_retries": 3,
    "concurrency": 2
  }'
```

#### Auto-detect Mode Job

```bash
# Will use immediate mode (no webhook_url)
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "auto-immediate-003",
    "url": "http://localhost:4000/immediate-api",
    "method": "GET",
    "queue_name": "auto_queue"
  }'

# Will use webhook mode (webhook_url provided)
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "auto-webhook-004",
    "url": "http://localhost:4000/initiate-call",
    "method": "POST",
    "body": "{\"prospectId\":\"test-004\"}",
    "queue_name": "auto_queue",
    "webhook_url": "http://localhost:3000/api/webhook"
  }'
```

### Job Status

**Endpoint:** `GET /api/job/status/{job_id}`

```bash
curl http://localhost:3000/api/job/status/job_abc123
```

**Response:**
```json
{
  "job_id": "job_abc123",
  "call_id": "immediate-job-001",
  "status": "completed",
  "queue_name": "immediate_queue",
  "completion_mode": "immediate",
  "created_at": "2025-06-02T10:30:00Z",
  "completed_at": "2025-06-02T10:30:05Z",
  "response": {
    "status_code": 200,
    "duration": "1.234s"
  }
}
```

### Queue Statistics

**Endpoint:** `GET /api/queue/stats`

```bash
# All queues
curl http://localhost:3000/api/queue/stats

# Specific queue
curl http://localhost:3000/api/queue/stats?queue=immediate_queue
```

**Response:**
```json
{
  "queue_name": "immediate_queue",
  "workers_active": 3,
  "pending_jobs": 0,
  "active_jobs": 2,
  "completed_jobs": 157,
  "failed_jobs": 3,
  "total_processed": 160
}
```

### Webhook Endpoint

**Endpoint:** `POST /api/webhook`

Used by external services to confirm job completion.

```bash
curl -X POST http://localhost:3000/api/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "callId": "external-call-id-789",
    "status": "completed",
    "message": "Call processing completed successfully"
  }'
```

## 📊 Monitoring

### Asynq Dashboard

Access the monitoring dashboard at **http://localhost:8081**

**Features:**
- 📈 Real-time queue statistics
- 📋 Job listing and details
- 🔄 Active/pending/completed job counts
- ❌ Failed job analysis
- 📊 Performance metrics
- 🕐 Historical data

**Dashboard Sections:**
1. **Overview**: System-wide statistics
2. **Queues**: Per-queue metrics and worker status
3. **Jobs**: Individual job details and history
4. **Failed**: Failed job analysis and retry options
5. **Scheduled**: Future/delayed jobs
6. **Servers**: Worker server information

### Queue Statistics API

Monitor programmatically via REST API:

```bash
# System overview
curl http://localhost:3000/api/system/stats

# Queue details
curl http://localhost:3000/api/queue/stats?queue=webhook_queue

# Job details
curl http://localhost:3000/api/job/status/job_abc123
```

## 🧪 Testing

### Basic Functionality Test

```bash
# Test immediate completion
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-immediate-'$(date +%s)'",
    "url": "http://httpbin.org/delay/2",
    "method": "GET",
    "queue_name": "test_immediate",
    "completion_mode": "immediate"
  }'

# Test webhook completion
curl -X POST http://localhost:3000/api/schedule-job \
  -H "Content-Type: application/json" \
  -d '{
    "call_id": "test-webhook-'$(date +%s)'",
    "url": "http://localhost:4000/initiate-call",
    "method": "POST",
    "body": "{\"prospectId\":\"test-'$(date +%s)'\"}",
    "queue_name": "test_webhook",
    "completion_mode": "webhook",
    "webhook_url": "http://localhost:3000/api/webhook"
  }'
```

### Load Testing

Use the provided test script for comprehensive testing:

```bash
# Install test dependencies
npm install axios uuid

# Run basic completion mode test
node test-script.js

# Run auto-restart test
node test-script.js --auto-restart

# Run individual mode tests
node test-script.js --immediate
node test-script.js --webhook
```

**Test Configurations:**
- 40 immediate completion queues
- 40 webhook completion queues
- 20 auto-detect queues
- Variable job counts (5-35 per queue)
- Variable concurrency (1-6 workers)

### Performance Benchmarks

**Expected Performance:**
- **Immediate Mode**: 50-100 jobs/second
- **Webhook Mode**: 10-30 jobs/second (depends on external service)
- **Memory Usage**: ~50-100MB under normal load
- **Redis Connections**: Up to 50 concurrent connections

## 🔧 Troubleshooting

### Common Issues

#### 1. Redis I/O Timeout Errors

**Symptoms:**
```
ERROR: Failed to write server state data: UNKNOWN: redis command error: SADD failed: i/o timeout
```

**Solutions:**
```bash
# Increase Redis timeouts
redis-cli CONFIG SET timeout 300
redis-cli CONFIG SET tcp-keepalive 60

# Reduce load
export DEFAULT_CONCURRENCY=3
export REDIS_POOL_SIZE=30
```

#### 2. Port Already in Use

**Symptoms:**
```
bind: Only one usage of each socket address is normally permitted
```

**Solutions:**
```bash
# Find and kill process
netstat -ano | findstr :8080
taskkill /PID <PID> /F

# Use different port
asynqmon --redis-addr=127.0.0.1:6379 --port=8081
```

#### 3. Docker Connection Issues

**Symptoms:**
```
host.docker.internal: no such host
```

**Solutions:**
```bash
# Use actual IP
ipconfig | findstr IPv4
docker run --rm --name asynqmon -p 8081:8080 hibiken/asynqmon --redis-addr=192.168.1.100:6379

# Use Docker network
docker run --name redis -d -p 6379:6379 redis:alpine
docker run --rm --name asynqmon -p 8081:8080 --link redis:redis hibiken/asynqmon --redis-addr=redis:6379
```

#### 4. Jobs Stuck in Processing

**Check:**
```bash
# Check worker status
curl http://localhost:3000/api/queue/stats

# Check Asynq dashboard
open http://localhost:8081

# Check Redis directly
redis-cli KEYS "asynq:*"
```

#### 5. Webhook Timeouts

**Symptoms:**
Jobs stuck in "awaiting webhook" status

**Solutions:**
```bash
# Increase webhook timeout
export WEBHOOK_TIMEOUT_SECONDS=300

# Check external service
curl http://localhost:4000/health

# Manual webhook confirmation
curl -X POST http://localhost:3000/api/webhook \
  -H "Content-Type: application/json" \
  -d '{"callId":"stuck-call-id","status":"completed"}'
```

### Debug Commands

```bash
# Check Redis connection
redis-cli ping

# Monitor Redis operations
redis-cli MONITOR

# Check queue contents
redis-cli KEYS "asynq:*"

# View job details
redis-cli GET "job:job_abc123"

# Check system resources
docker stats
```

### Log Analysis

**Log Patterns to Monitor:**

```bash
# Successful processing
grep "Job completed" logs/app.log

# Failed jobs
grep "Job permanently failed" logs/app.log

# Redis errors
grep "Redis" logs/app.log | grep ERROR

# Webhook confirmations
grep "Webhook confirmed" logs/app.log
```

## 📚 Advanced Usage

### Custom Retry Strategies

```go
// Custom backoff strategy
BackoffDelay: func(n int, err error, task *asynq.Task) time.Duration {
    return time.Duration(n*n) * time.Second // Quadratic backoff
}
```

### Queue Prioritization

```bash
# High priority queue
curl -X POST http://localhost:3000/api/schedule-job \
  -d '{"queue_name": "urgent_queue", "concurrency": 10}'

# Low priority queue  
curl -X POST http://localhost:3000/api/schedule-job \
  -d '{"queue_name": "background_queue", "concurrency": 1}'
```

### Scheduled Jobs

```bash
# Schedule job for future execution
curl -X POST http://localhost:3000/api/schedule-job \
  -d '{
    "call_id": "future-job",
    "url": "http://example.com/api",
    "method": "POST",
    "queue_name": "scheduled_queue",
    "schedule_time": "2025-06-03T10:00:00Z"
  }'
```

## 📈 Production Deployment

### Scaling Recommendations

**Redis:**
- Use Redis Cluster for high availability
- Enable persistence with AOF
- Monitor memory usage and set appropriate maxmemory

**Application:**
- Deploy multiple instances behind load balancer
- Use environment-specific configurations
- Implement health checks

**Monitoring:**
- Set up alerting for failed jobs
- Monitor Redis connection pool usage
- Track job processing latency

### Health Checks

```bash
# Application health
curl http://localhost:3000/health

# Redis health
redis-cli ping

# Queue health
curl http://localhost:3000/api/queue/stats
```

---

## 📞 Support

For issues and questions:
1. Check the [Troubleshooting](#troubleshooting) section
2. Review logs and monitoring dashboard
3. Test with simplified configurations
4. Check Redis connectivity and performance

## 📄 License

This project is licensed under the MIT License.

---

**Happy Queue Processing! 🚀**
