package redis

import (
	"context"
	"fmt"
	"log"
	"time"

	"queue-handler-2/internal/config"

	"github.com/go-redis/redis/v8"
	"github.com/hibiken/asynq"
)

// Client wraps Redis functionality for both direct use and Asynq
type Client struct {
	rdb      *redis.Client
	ctx      context.Context
	asynqOpt asynq.RedisClientOpt
	config   *config.Config
}

// NewClient creates a new Redis client optimized for high load
func NewClient(cfg *config.Config) *Client {
	// Create optimized Redis client options for high load scenarios
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.GetRedisAddr(),
		Password: "", // no password
		DB:       0,  // default DB

		// Connection pool settings - CRITICAL for high load
		PoolSize:     50, // Increased from default 10
		MinIdleConns: 10, // Keep minimum connections alive
		MaxRetries:   5,  // Retry failed commands

		// Timeout settings - FIXES I/O timeout issues
		DialTimeout:  30 * time.Second, // Connection establishment timeout
		ReadTimeout:  60 * time.Second, // Read operation timeout (increased)
		WriteTimeout: 60 * time.Second, // Write operation timeout (increased)
		PoolTimeout:  60 * time.Second, // Pool checkout timeout

		// Connection lifecycle management
		IdleTimeout:        300 * time.Second, // Close idle connections after 5 min
		IdleCheckFrequency: 60 * time.Second,  // Check for idle connections every minute
		MaxConnAge:         30 * time.Minute,  // Refresh connections every 30 min
	})

	ctx := context.Background()

	// Test connection with retry logic
	maxRetries := 3
	var err error
	for i := 0; i < maxRetries; i++ {
		_, err = rdb.Ping(ctx).Result()
		if err == nil {
			break
		}
		log.Printf("⚠️ Redis connection attempt %d failed: %v", i+1, err)
		if i < maxRetries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}

	if err != nil {
		log.Fatalf("❌ Failed to connect to Redis after %d attempts: %v", maxRetries, err)
	}

	// Create optimized Asynq Redis options
	asynqOpt := asynq.RedisClientOpt{
		Addr:     cfg.GetRedisAddr(),
		Password: "",
		DB:       0,

		// Add connection pool settings for Asynq
		PoolSize:  50,
		TLSConfig: nil,

		// Timeout settings for Asynq operations
		DialTimeout:  30 * time.Second,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Printf("✅ Connected to Redis at %s with optimized settings", cfg.GetRedisAddr())
	log.Printf("🔧 Pool size: %d, Timeouts: %v/%v/%v", 50, 30*time.Second, 60*time.Second, 60*time.Second)

	return &Client{
		rdb:      rdb,
		ctx:      ctx,
		asynqOpt: asynqOpt,
		config:   cfg,
	}
}

// GetRedisClient returns the raw Redis client
func (c *Client) GetRedisClient() *redis.Client {
	return c.rdb
}

// GetAsynqOptions returns Asynq Redis options
func (c *Client) GetAsynqOptions() asynq.RedisClientOpt {
	return c.asynqOpt
}

// GetContext returns the context
func (c *Client) GetContext() context.Context {
	return c.ctx
}

// executeWithRetry executes Redis operations with retry logic
func (c *Client) executeWithRetry(operation func() error, operationName string) error {
	maxRetries := 3
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		err = operation()
		if err == nil {
			return nil
		}

		// Log the retry attempt
		if attempt < maxRetries-1 {
			log.Printf("⚠️ %s failed (attempt %d/%d): %v, retrying...", operationName, attempt+1, maxRetries, err)
			// Exponential backoff
			backoff := time.Duration(attempt+1) * 100 * time.Millisecond
			time.Sleep(backoff)
		}
	}

	log.Printf("❌ %s failed after %d attempts: %v", operationName, maxRetries, err)
	return fmt.Errorf("%s failed after %d attempts: %w", operationName, maxRetries, err)
}

// Job state management methods with retry logic

// StoreJobMetadata stores job metadata in Redis with retry
func (c *Client) StoreJobMetadata(jobID string, jobData string) error {
	return c.executeWithRetry(func() error {
		key := c.jobKey(jobID)
		return c.rdb.Set(c.ctx, key, jobData, c.config.JobTTL).Err()
	}, fmt.Sprintf("StoreJobMetadata(%s)", jobID))
}

// GetJobMetadata retrieves job metadata from Redis with retry
func (c *Client) GetJobMetadata(jobID string) (string, error) {
	var result string
	err := c.executeWithRetry(func() error {
		key := c.jobKey(jobID)
		var getErr error
		result, getErr = c.rdb.Get(c.ctx, key).Result()
		return getErr
	}, fmt.Sprintf("GetJobMetadata(%s)", jobID))
	return result, err
}

// DeleteJobMetadata removes job metadata from Redis with retry
func (c *Client) DeleteJobMetadata(jobID string) error {
	return c.executeWithRetry(func() error {
		key := c.jobKey(jobID)
		return c.rdb.Del(c.ctx, key).Err()
	}, fmt.Sprintf("DeleteJobMetadata(%s)", jobID))
}

// CreateCallIDMapping creates CallID -> JobID mapping with retry
func (c *Client) CreateCallIDMapping(callID, jobID string) error {
	return c.executeWithRetry(func() error {
		key := c.callIDKey(callID)
		return c.rdb.Set(c.ctx, key, jobID, c.config.JobTTL).Err()
	}, fmt.Sprintf("CreateCallIDMapping(%s->%s)", callID, jobID))
}

// GetJobIDByCallID retrieves JobID by CallID with retry
func (c *Client) GetJobIDByCallID(callID string) (string, error) {
	var result string
	err := c.executeWithRetry(func() error {
		key := c.callIDKey(callID)
		var getErr error
		result, getErr = c.rdb.Get(c.ctx, key).Result()
		return getErr
	}, fmt.Sprintf("GetJobIDByCallID(%s)", callID))
	return result, err
}

// Webhook tracking methods with retry logic

// MarkJobAwaitingWebhook marks a job as awaiting webhook confirmation with retry
func (c *Client) MarkJobAwaitingWebhook(jobID string, timeout time.Duration) error {
	return c.executeWithRetry(func() error {
		key := c.webhookKey(jobID)
		expiresAt := time.Now().Add(timeout)
		return c.rdb.Set(c.ctx, key, expiresAt.Unix(), timeout).Err()
	}, fmt.Sprintf("MarkJobAwaitingWebhook(%s)", jobID))
}

// RemoveWebhookTracking removes webhook tracking for a job with retry
func (c *Client) RemoveWebhookTracking(jobID string) error {
	return c.executeWithRetry(func() error {
		key := c.webhookKey(jobID)
		return c.rdb.Del(c.ctx, key).Err()
	}, fmt.Sprintf("RemoveWebhookTracking(%s)", jobID))
}

// GetWebhookStatus checks if webhook has been received for a job with retry
func (c *Client) GetWebhookStatus(jobID, externalCallID string) (string, error) {
	var status string
	err := c.executeWithRetry(func() error {
		// Check webhook status by jobID
		key := fmt.Sprintf("webhook_status:%s", jobID)
		result, err := c.rdb.Get(c.ctx, key).Result()
		if err == nil && result != "" {
			status = result
			return nil
		}

		// If not found by jobID, try by external callID as fallback
		if externalCallID != "" && externalCallID != jobID {
			callKey := fmt.Sprintf("webhook_status:call:%s", externalCallID)
			result, err := c.rdb.Get(c.ctx, callKey).Result()
			if err == nil && result != "" {
				status = result
				return nil
			}
		}

		// If key doesn't exist (redis.Nil error), return "pending"
		if err == redis.Nil {
			status = "pending"
			return nil
		}

		// Return actual error if it's not a "key not found" error
		if err != nil {
			return err
		}

		// Default to pending if status is empty
		status = "pending"
		return nil
	}, fmt.Sprintf("GetWebhookStatus(%s,%s)", jobID, externalCallID))

	return status, err
}

// GetPendingWebhookJobs returns jobs awaiting webhook confirmation with retry
func (c *Client) GetPendingWebhookJobs() ([]string, error) {
	var jobIDs []string
	err := c.executeWithRetry(func() error {
		pattern := "webhook_pending:*"
		keys, err := c.rdb.Keys(c.ctx, pattern).Result()
		if err != nil {
			return err
		}

		jobIDs = make([]string, 0, len(keys))
		for _, key := range keys {
			jobID := key[len("webhook_pending:"):]
			jobIDs = append(jobIDs, jobID)
		}
		return nil
	}, "GetPendingWebhookJobs")

	return jobIDs, err
}

// Queue operations (for statistics and monitoring) with retry logic

// IncrementStat increments a queue statistic counter with retry
func (c *Client) IncrementStat(queueName, statType string) error {
	return c.executeWithRetry(func() error {
		key := c.statKey(queueName, statType)
		return c.rdb.Incr(c.ctx, key).Err()
	}, fmt.Sprintf("IncrementStat(%s,%s)", queueName, statType))
}

// DecrementStat decrements a queue statistic counter with retry
func (c *Client) DecrementStat(queueName, statType string) error {
	return c.executeWithRetry(func() error {
		key := c.statKey(queueName, statType)
		return c.rdb.Decr(c.ctx, key).Err()
	}, fmt.Sprintf("DecrementStat(%s,%s)", queueName, statType))
}

// GetStat gets a queue statistic value with retry
func (c *Client) GetStat(queueName, statType string) (int64, error) {
	var result int64
	err := c.executeWithRetry(func() error {
		key := c.statKey(queueName, statType)
		int64Value, err := c.rdb.Get(c.ctx, key).Int64()
		if err != nil {
			if err == redis.Nil {
				result = 0
				return nil
			}
			return err
		}
		result = int64Value
		return nil
	}, fmt.Sprintf("GetStat(%s,%s)", queueName, statType))

	return result, err
}

// Generic Redis operations with retry logic

// Set sets a key-value pair with optional expiration and retry
func (c *Client) Set(key string, value interface{}, expiration time.Duration) error {
	return c.executeWithRetry(func() error {
		return c.rdb.Set(c.ctx, key, value, expiration).Err()
	}, fmt.Sprintf("Set(%s)", key))
}

// Get gets a value by key with retry
func (c *Client) Get(key string) (string, error) {
	var result string
	err := c.executeWithRetry(func() error {
		var getErr error
		result, getErr = c.rdb.Get(c.ctx, key).Result()
		return getErr
	}, fmt.Sprintf("Get(%s)", key))
	return result, err
}

// Del deletes keys with retry
func (c *Client) Del(keys ...string) error {
	return c.executeWithRetry(func() error {
		return c.rdb.Del(c.ctx, keys...).Err()
	}, fmt.Sprintf("Del(%v)", keys))
}

// Exists checks if keys exist with retry
func (c *Client) Exists(keys ...string) (int64, error) {
	var result int64
	err := c.executeWithRetry(func() error {
		var existsErr error
		result, existsErr = c.rdb.Exists(c.ctx, keys...).Result()
		return existsErr
	}, fmt.Sprintf("Exists(%v)", keys))
	return result, err
}

// TTL gets the time to live for a key with retry
func (c *Client) TTL(key string) (time.Duration, error) {
	var result time.Duration
	err := c.executeWithRetry(func() error {
		var ttlErr error
		result, ttlErr = c.rdb.TTL(c.ctx, key).Result()
		return ttlErr
	}, fmt.Sprintf("TTL(%s)", key))
	return result, err
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Helper methods for key generation

func (c *Client) jobKey(jobID string) string {
	return fmt.Sprintf("job:%s", jobID)
}

func (c *Client) callIDKey(callID string) string {
	return fmt.Sprintf("call_id:%s", callID)
}

func (c *Client) webhookKey(jobID string) string {
	return fmt.Sprintf("webhook_pending:%s", jobID)
}

func (c *Client) statKey(queueName, statType string) string {
	return fmt.Sprintf("stats:%s:%s", queueName, statType)
}

// Health check with retry
func (c *Client) Ping() error {
	return c.executeWithRetry(func() error {
		return c.rdb.Ping(c.ctx).Err()
	}, "Ping")
}

// GetConnectionStats returns Redis connection pool statistics
func (c *Client) GetConnectionStats() map[string]interface{} {
	stats := c.rdb.PoolStats()
	return map[string]interface{}{
		"hits":        stats.Hits,
		"misses":      stats.Misses,
		"timeouts":    stats.Timeouts,
		"total_conns": stats.TotalConns,
		"idle_conns":  stats.IdleConns,
		"stale_conns": stats.StaleConns,
	}
}
