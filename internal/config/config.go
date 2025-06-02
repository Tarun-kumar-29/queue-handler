package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server settings
	RedisHost  string
	RedisPort  int
	ServerPort int

	// HTTP settings
	HTTPTimeout time.Duration

	// Queue settings
	DefaultConcurrency int
	MaxRetries         int
	BackoffDelay       time.Duration

	// Timeout settings
	ShutdownTimeout time.Duration
	WebhookTimeout  time.Duration
	JobTTL          time.Duration

	// NEW: Redis optimization settings
	RedisPoolSize     int
	RedisMinIdleConns int
	RedisMaxRetries   int
	RedisDialTimeout  time.Duration
	RedisReadTimeout  time.Duration
	RedisWriteTimeout time.Duration
	RedisPoolTimeout  time.Duration
	RedisIdleTimeout  time.Duration
	RedisMaxConnAge   time.Duration
}

func LoadConfig() *Config {
	return &Config{
		// Existing settings
		RedisHost:          getEnv("REDIS_HOST", "127.0.0.1"),
		RedisPort:          getEnvInt("REDIS_PORT", 6379),
		ServerPort:         getEnvInt("SERVER_PORT", 3000),
		HTTPTimeout:        time.Duration(getEnvInt("HTTP_TIMEOUT_SECONDS", 300)) * time.Second,
		DefaultConcurrency: getEnvInt("DEFAULT_CONCURRENCY", 5), // Reduced from 10 for Redis stability
		MaxRetries:         getEnvInt("MAX_RETRIES", 3),
		BackoffDelay:       time.Duration(getEnvInt("BACKOFF_DELAY_MS", 5000)) * time.Millisecond, // Reduced
		ShutdownTimeout:    time.Duration(getEnvInt("SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,
		WebhookTimeout:     time.Duration(getEnvInt("WEBHOOK_TIMEOUT_SECONDS", 120)) * time.Second, // Increased
		JobTTL:             time.Duration(getEnvInt("JOB_TTL_HOURS", 24)) * time.Hour,

		// NEW: Redis optimization settings
		RedisPoolSize:     getEnvInt("REDIS_POOL_SIZE", 50),                                          // Connection pool size
		RedisMinIdleConns: getEnvInt("REDIS_MIN_IDLE_CONNS", 10),                                     // Minimum idle connections
		RedisMaxRetries:   getEnvInt("REDIS_MAX_RETRIES", 5),                                         // Redis operation retries
		RedisDialTimeout:  time.Duration(getEnvInt("REDIS_DIAL_TIMEOUT_SECONDS", 30)) * time.Second,  // Connection timeout
		RedisReadTimeout:  time.Duration(getEnvInt("REDIS_READ_TIMEOUT_SECONDS", 60)) * time.Second,  // Read timeout
		RedisWriteTimeout: time.Duration(getEnvInt("REDIS_WRITE_TIMEOUT_SECONDS", 60)) * time.Second, // Write timeout
		RedisPoolTimeout:  time.Duration(getEnvInt("REDIS_POOL_TIMEOUT_SECONDS", 60)) * time.Second,  // Pool checkout timeout
		RedisIdleTimeout:  time.Duration(getEnvInt("REDIS_IDLE_TIMEOUT_SECONDS", 300)) * time.Second, // Idle connection timeout
		RedisMaxConnAge:   time.Duration(getEnvInt("REDIS_MAX_CONN_AGE_MINUTES", 30)) * time.Minute,  // Max connection age
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// GetRedisAddr returns the Redis connection string
func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", c.RedisHost, c.RedisPort)
}

// LogConfig logs the current configuration (useful for debugging)
func (c *Config) LogConfig() {
	fmt.Printf("📋 Configuration Loaded:\n")
	fmt.Printf("   Redis: %s (pool: %d, timeouts: %v/%v/%v)\n",
		c.GetRedisAddr(), c.RedisPoolSize, c.RedisDialTimeout, c.RedisReadTimeout, c.RedisWriteTimeout)
	fmt.Printf("   Server: :%d (concurrency: %d, retries: %d)\n",
		c.ServerPort, c.DefaultConcurrency, c.MaxRetries)
	fmt.Printf("   Timeouts: HTTP=%v, Webhook=%v, Shutdown=%v\n",
		c.HTTPTimeout, c.WebhookTimeout, c.ShutdownTimeout)
}
