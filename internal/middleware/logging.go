package middleware

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// LoggingMiddleware logs HTTP requests and responses (same as before)
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a custom ResponseWriter to capture the status code
		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Process request
		next.ServeHTTP(lrw, r)

		// Log the request
		duration := time.Since(start)
		log.Printf("🌐 %s %s - %d - %v - %s",
			r.Method,
			r.RequestURI,
			lrw.statusCode,
			duration,
			r.RemoteAddr,
		)
	})
}

// loggingResponseWriter wraps http.ResponseWriter to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// CORSMiddleware adds CORS headers
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "3600")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware provides basic authentication (optional)
func AuthMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				// No authentication required
				next.ServeHTTP(w, r)
				return
			}

			// Check for API key in header
			providedKey := r.Header.Get("X-API-Key")
			if providedKey == "" {
				// Check Authorization header
				auth := r.Header.Get("Authorization")
				if auth != "" && len(auth) > 7 && auth[:7] == "Bearer " {
					providedKey = auth[7:]
				}
			}

			if providedKey != apiKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitMiddleware provides simple rate limiting (basic implementation)
func RateLimitMiddleware(requestsPerMinute int) func(http.Handler) http.Handler {
	if requestsPerMinute <= 0 {
		// No rate limiting
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	// Simple in-memory rate limiter (not suitable for production clustering)
	clients := make(map[string][]time.Time)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := r.RemoteAddr

			now := time.Now()
			cutoff := now.Add(-time.Minute)

			// Clean up old requests
			if requests, exists := clients[clientIP]; exists {
				var validRequests []time.Time
				for _, reqTime := range requests {
					if reqTime.After(cutoff) {
						validRequests = append(validRequests, reqTime)
					}
				}
				clients[clientIP] = validRequests
			}

			// Check rate limit
			if len(clients[clientIP]) >= requestsPerMinute {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			// Add current request
			clients[clientIP] = append(clients[clientIP], now)

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryMiddleware recovers from panics and logs them
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("🚨 Panic recovered: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestIDMiddleware adds a unique request ID to each request
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

// generateRequestID generates a simple request ID
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}