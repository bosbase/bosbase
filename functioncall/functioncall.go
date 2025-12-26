package functioncall

// functioncall.go
// High-concurrency, thread-safe HTTP client for FastAPI script execution service.
// Features: connection pooling, retry mechanism, rate limiting, circuit breaker, and metrics collection.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ==================== CONFIGURATION STRUCTURES ====================

// Config holds client configuration
type Config struct {
	BaseURL                 string        `json:"base_url"`                  // Base URL of the FastAPI service
	MaxIdleConns            int           `json:"max_idle_conns"`            // Maximum number of idle connections
	MaxIdleConnsPerHost     int           `json:"max_idle_conns_per_host"`   // Maximum idle connections per host
	IdleConnTimeout         time.Duration `json:"idle_conn_timeout"`         // Idle connection timeout
	RequestTimeout          time.Duration `json:"request_timeout"`           // Request timeout
	MaxRetries              int           `json:"max_retries"`               // Maximum retry attempts
	RetryDelay              time.Duration `json:"retry_delay"`               // Delay between retries
	RateLimitPerSecond      float64       `json:"rate_limit_per_second"`     // Rate limit per second (0 = unlimited)
	RateBurst               int           `json:"rate_burst"`                // Rate limiter burst capacity
	EnableMetrics           bool          `json:"enable_metrics"`            // Enable metrics collection
	EnableCircuitBreaker    bool          `json:"enable_circuit_breaker"`    // Enable circuit breaker
	CircuitFailureThreshold int           `json:"circuit_failure_threshold"` // Circuit breaker failure threshold
	CircuitResetTimeout     time.Duration `json:"circuit_reset_timeout"`     // Circuit breaker reset timeout
}

// DefaultConfig returns a default configuration with sensible values
func DefaultConfig() Config {
	baseURL := os.Getenv("FUNCTION_URL")
	// functions path fastapi service project,read source code functions/main.py
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	maxIdleConns := 10
	if connNumStr := os.Getenv("FUNCTION_CONN_NUM"); connNumStr != "" {
		if connNum, err := strconv.Atoi(connNumStr); err == nil {
			maxIdleConns = connNum
		}
	}

	return Config{
		BaseURL:                 baseURL,
		MaxIdleConns:            maxIdleConns,
		MaxIdleConnsPerHost:     10,
		IdleConnTimeout:         90 * time.Second,
		RequestTimeout:          30 * time.Second,
		MaxRetries:              3,
		RetryDelay:              100 * time.Millisecond,
		RateLimitPerSecond:      0, // 0 means unlimited by default
		RateBurst:               1, // Default burst size
		EnableMetrics:           true,
		EnableCircuitBreaker:    true,
		CircuitFailureThreshold: 10,
		CircuitResetTimeout:     30 * time.Second,
	}
}

// ==================== DATA MODELS ====================

// ScriptExecuteRequest represents a script execution request
type ScriptExecuteRequest struct {
	ScriptPath   string                 `json:"script_path,omitempty"`
	ScriptName   string                 `json:"script_name,omitempty"`
	FunctionName string                 `json:"function_name"`
	Args         []interface{}          `json:"args,omitempty"`
	Kwargs       map[string]interface{} `json:"kwargs,omitempty"`
	Timeout      *int                   `json:"timeout,omitempty"`
}

// ScriptExecuteResponse represents a script execution response
type ScriptExecuteResponse struct {
	Success            bool        `json:"success"`
	RequestID          string      `json:"request_id,omitempty"`
	Result             interface{} `json:"result,omitempty"`
	Error              string      `json:"error,omitempty"`
	ExecutionTime      float64     `json:"execution_time"`
	Script             string      `json:"script,omitempty"`
	Function           string      `json:"function,omitempty"`
	AvailableFunctions []string    `json:"available_functions,omitempty"`
}

// ScriptInfo represents script information
type ScriptInfo struct {
	Name         string                 `json:"name"`
	Path         string                 `json:"path"`
	Status       string                 `json:"status"`
	Functions    []string               `json:"functions"`
	Metadata     map[string]interface{} `json:"metadata"`
	LoadTime     string                 `json:"load_time,omitempty"`
	CallCount    int                    `json:"call_count"`
	AvgTime      float64                `json:"avg_time"`
	LastCallTime string                 `json:"last_call_time,omitempty"`
}

// HealthResponse represents health check response
type HealthResponse struct {
	Status              string  `json:"status"`
	Uptime              float64 `json:"uptime"`
	ScriptsLoaded       int     `json:"scripts_loaded"`
	DirectoriesWatching int     `json:"directories_watching"`
	TotalRequests       int     `json:"total_requests"`
	SuccessRequests     int     `json:"success_requests,omitempty"`
	FailedRequests      int     `json:"failed_requests,omitempty"`
	Timestamp           string  `json:"timestamp"`
}

// MetricsResponse represents metrics response
type MetricsResponse struct {
	Scripts struct {
		Total  int `json:"total"`
		Loaded int `json:"loaded"`
		Error  int `json:"error"`
	} `json:"scripts"`
	Executions struct {
		Total   int     `json:"total"`
		Success int     `json:"success"`
		Failed  int     `json:"failed"`
		AvgTime float64 `json:"avg_time"`
	} `json:"executions"`
	Directories struct {
		Watching int `json:"watching"`
	} `json:"directories"`
	Performance struct {
		Uptime            float64 `json:"uptime"`
		RequestsPerSecond float64 `json:"requests_per_second"`
	} `json:"performance"`
}

// ErrorResponse represents an error response from the server
type ErrorResponse struct {
	Detail string `json:"detail"`
}

// ==================== CIRCUIT BREAKER ====================

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu               sync.RWMutex
	failureThreshold int           // Failure threshold
	resetTimeout     time.Duration // Reset timeout
	failures         int           // Current failure count
	lastFailureTime  time.Time     // Time of last failure
	state            string        // State: closed, open, half-open
	successThreshold int           // Success threshold for half-open state
	successes        int           // Current success count
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		state:            "closed",
		successThreshold: 3,
	}
}

// Allow checks if a request is allowed
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	// If circuit is open, check if reset timeout has passed
	if cb.state == "open" {
		if time.Since(cb.lastFailureTime) >= cb.resetTimeout {
			cb.mu.RUnlock()
			cb.mu.Lock()
			cb.state = "half-open"
			cb.successes = 0
			cb.mu.Unlock()
			cb.mu.RLock()
			return true
		}
		return false
	}

	return true
}

// Success records a successful request
func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "half-open" {
		cb.successes++
		if cb.successes >= cb.successThreshold {
			cb.state = "closed"
			cb.failures = 0
		}
	}
}

// Failure records a failed request
func (cb *CircuitBreaker) Failure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailureTime = time.Now()

	if cb.state == "half-open" {
		cb.state = "open"
	} else if cb.state == "closed" && cb.failures >= cb.failureThreshold {
		cb.state = "open"
	}
}

// State returns the current state
func (cb *CircuitBreaker) State() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// ==================== CLIENT MAIN STRUCTURE ====================

// Client represents the HTTP client
type Client struct {
	config         Config
	httpClient     *http.Client
	rateLimiter    *rate.Limiter
	circuitBreaker *CircuitBreaker
	metrics        *MetricsCollector
	mu             sync.RWMutex // For protecting configuration changes
	logger         *log.Logger
}

// MetricsCollector collects client metrics
type MetricsCollector struct {
	mu                   sync.RWMutex
	totalRequests        int64
	successRequests      int64
	failedRequests       int64
	totalLatency         time.Duration
	requestsByMethod     map[string]int64
	requestsByStatus     map[int]int64
	circuitBreakerEvents map[string]int64
	startTime            time.Time
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		requestsByMethod:     make(map[string]int64),
		requestsByStatus:     make(map[int]int64),
		circuitBreakerEvents: make(map[string]int64),
		startTime:            time.Now(),
	}
}

// RecordRequest records a request in metrics
func (mc *MetricsCollector) RecordRequest(method string, duration time.Duration, status int, success bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.totalRequests++
	mc.totalLatency += duration

	if success {
		mc.successRequests++
	} else {
		mc.failedRequests++
	}

	mc.requestsByMethod[method]++
	mc.requestsByStatus[status]++
}

// RecordCircuitBreakerEvent records a circuit breaker event
func (mc *MetricsCollector) RecordCircuitBreakerEvent(event string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.circuitBreakerEvents[event]++
}

// GetMetrics returns the collected metrics
func (mc *MetricsCollector) GetMetrics() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	uptime := time.Since(mc.startTime).Seconds()
	avgLatency := 0.0
	if mc.totalRequests > 0 {
		avgLatency = mc.totalLatency.Seconds() / float64(mc.totalRequests)
	}

	return map[string]interface{}{
		"total_requests":         mc.totalRequests,
		"success_requests":       mc.successRequests,
		"failed_requests":        mc.failedRequests,
		"avg_latency_sec":        avgLatency,
		"requests_per_sec":       float64(mc.totalRequests) / uptime,
		"uptime_sec":             uptime,
		"requests_by_method":     mc.requestsByMethod,
		"requests_by_status":     mc.requestsByStatus,
		"circuit_breaker_events": mc.circuitBreakerEvents,
	}
}

// ==================== CLIENT CONSTRUCTORS ====================

// NewClient creates a new client instance
func NewClient(config Config) (*Client, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	// Create HTTP transport
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,
	}

	// Create HTTP client
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   config.RequestTimeout,
	}

	// Create rate limiter
	var rateLimiter *rate.Limiter
	if config.RateLimitPerSecond > 0 {
		// Use token bucket rate limiter with specified limit
		rateLimiter = rate.NewLimiter(rate.Limit(config.RateLimitPerSecond), config.RateBurst)
	} else {
		// Unlimited rate - use infinite limiter
		rateLimiter = rate.NewLimiter(rate.Inf, 1)
	}

	// Create circuit breaker
	var circuitBreaker *CircuitBreaker
	if config.EnableCircuitBreaker {
		circuitBreaker = NewCircuitBreaker(config.CircuitFailureThreshold, config.CircuitResetTimeout)
	}

	// Create metrics collector
	var metrics *MetricsCollector
	if config.EnableMetrics {
		metrics = NewMetricsCollector()
	}

	// Create logger
	logger := log.New(log.Writer(), "[ScriptExecutor] ", log.LstdFlags|log.Lshortfile)

	return &Client{
		config:         config,
		httpClient:     httpClient,
		rateLimiter:    rateLimiter,
		circuitBreaker: circuitBreaker,
		metrics:        metrics,
		logger:         logger,
	}, nil
}

// ==================== CORE HTTP METHODS ====================

// doRequest executes an HTTP request
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	// Check circuit breaker
	if c.circuitBreaker != nil && !c.circuitBreaker.Allow() {
		if c.metrics != nil {
			c.metrics.RecordCircuitBreakerEvent("circuit_open_blocked")
		}
		return nil, fmt.Errorf("circuit breaker is open, request blocked")
	}

	// Apply rate limiting
	if c.rateLimiter != nil {
		err := c.rateLimiter.Wait(ctx)
		if err != nil {
			if c.circuitBreaker != nil {
				c.circuitBreaker.Failure()
			}
			return nil, fmt.Errorf("rate limit wait failed: %w", err)
		}
	}

	// Build URL
	url := c.config.BaseURL + endpoint

	// Prepare request body
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Go-ScriptExecutor-Client/1.0")

	// Record start time for metrics
	startTime := time.Now()

	// Execute request with retry mechanism
	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Retry delay with exponential backoff
			backoffDelay := c.config.RetryDelay * time.Duration(1<<uint(attempt-1)) // Exponential backoff
			if backoffDelay > 5*time.Second {
				backoffDelay = 5 * time.Second
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffDelay):
				// Continue with retry
			}

			c.logger.Printf("Retrying request to %s (attempt %d/%d)", endpoint, attempt, c.config.MaxRetries)
		}

		resp, err = c.httpClient.Do(req)
		if err == nil {
			// Check if status code indicates success
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break // Success
			}

			// For non-2xx status codes, we might want to retry
			statusCode := resp.StatusCode
			if !isRetryableStatusCode(statusCode) {
				// Don't retry for non-retryable status codes
				break
			}

			// Close body for retry
			resp.Body.Close()
			resp = nil
			lastErr = fmt.Errorf("server returned status %d", statusCode)
		} else {
			lastErr = err
		}

		// If this was the last attempt, break
		if attempt == c.config.MaxRetries {
			break
		}

		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Continue with next attempt
		}
	}

	// Handle request failure
	if resp == nil {
		if c.circuitBreaker != nil {
			c.circuitBreaker.Failure()
			if c.metrics != nil {
				c.metrics.RecordCircuitBreakerEvent("request_failure")
			}
		}

		if lastErr != nil {
			return nil, fmt.Errorf("request failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
		}
		return nil, fmt.Errorf("request failed after %d attempts", c.config.MaxRetries+1)
	}

	// Record metrics
	if c.metrics != nil {
		duration := time.Since(startTime)
		success := resp.StatusCode >= 200 && resp.StatusCode < 300
		c.metrics.RecordRequest(method, duration, resp.StatusCode, success)
	}

	// Update circuit breaker
	if c.circuitBreaker != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			c.circuitBreaker.Success()
			if c.metrics != nil {
				c.metrics.RecordCircuitBreakerEvent("request_success")
			}
		} else {
			c.circuitBreaker.Failure()
			if c.metrics != nil {
				c.metrics.RecordCircuitBreakerEvent("request_failure")
			}
		}
	}

	return resp, nil
}

// isRetryableStatusCode checks if a status code is retryable
func isRetryableStatusCode(statusCode int) bool {
	// Retry on 5xx server errors and 429 (Too Many Requests)
	return statusCode >= 500 || statusCode == 429
}

// decodeResponse decodes the HTTP response
func (c *Client) decodeResponse(resp *http.Response, v interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode >= 400 {
		var errorResp ErrorResponse
		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Detail != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errorResp.Detail)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Decode successful response
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w, body: %s", err, string(body))
		}
	}

	return nil
}

// ==================== PUBLIC API METHODS ====================

// HealthCheck performs a health check
func (c *Client) HealthCheck(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/health", nil)
	if err != nil {
		return nil, err
	}

	var healthResp HealthResponse
	if err := c.decodeResponse(resp, &healthResp); err != nil {
		return nil, err
	}

	return &healthResp, nil
}

// ExecuteScript executes a script function
func (c *Client) ExecuteScript(ctx context.Context, req ScriptExecuteRequest) (*ScriptExecuteResponse, error) {
	// Validate function name
	if req.FunctionName == "" {
		return &ScriptExecuteResponse{
			Success: false,
			Error:   "function_name is required",
		}, nil
	}

	resp, err := c.doRequest(ctx, "POST", "/execute", req)
	if err != nil {
		return &ScriptExecuteResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	var execResp ScriptExecuteResponse
	if err := c.decodeResponse(resp, &execResp); err != nil {
		return &ScriptExecuteResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &execResp, nil
}

// ExecuteScriptWithTimeout executes a script with custom timeout
func (c *Client) ExecuteScriptWithTimeout(ctx context.Context, req ScriptExecuteRequest, timeout time.Duration) (*ScriptExecuteResponse, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.ExecuteScript(ctxWithTimeout, req)
}

// ListScripts lists all loaded scripts
func (c *Client) ListScripts(ctx context.Context) ([]ScriptInfo, error) {
	resp, err := c.doRequest(ctx, "GET", "/scripts", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Scripts []ScriptInfo `json:"scripts"`
		Count   int          `json:"count"`
	}

	if err := c.decodeResponse(resp, &result); err != nil {
		return nil, err
	}

	return result.Scripts, nil
}

// GetScriptInfo gets detailed information about a script
func (c *Client) GetScriptInfo(ctx context.Context, scriptName string) (*ScriptInfo, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/scripts/%s", scriptName), nil)
	if err != nil {
		return nil, err
	}

	var scriptInfo ScriptInfo
	if err := c.decodeResponse(resp, &scriptInfo); err != nil {
		return nil, err
	}

	return &scriptInfo, nil
}

// AddWatchDirectory adds a directory to watch
func (c *Client) AddWatchDirectory(ctx context.Context, directory string, recursive bool) (bool, error) {
	req := map[string]interface{}{
		"directory": directory,
		"recursive": recursive,
	}

	resp, err := c.doRequest(ctx, "POST", "/directories", req)
	if err != nil {
		return false, err
	}

	var result struct {
		Success   bool   `json:"success"`
		Directory string `json:"directory"`
	}

	if err := c.decodeResponse(resp, &result); err != nil {
		return false, err
	}

	return result.Success, nil
}

// RemoveWatchDirectory removes a directory from watch
func (c *Client) RemoveWatchDirectory(ctx context.Context, directoryPath string) (bool, error) {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/directories/%s", directoryPath), nil)
	if err != nil {
		return false, err
	}

	var result struct {
		Success   bool   `json:"success"`
		Directory string `json:"directory"`
	}

	if err := c.decodeResponse(resp, &result); err != nil {
		return false, err
	}

	return result.Success, nil
}

// ReloadScript reloads a script
func (c *Client) ReloadScript(ctx context.Context, scriptName string) (bool, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/scripts/%s/reload", scriptName), nil)
	if err != nil {
		return false, err
	}

	var result struct {
		Success bool   `json:"success"`
		Script  string `json:"script"`
	}

	if err := c.decodeResponse(resp, &result); err != nil {
		return false, err
	}

	return result.Success, nil
}

// ReloadAllScripts reloads all scripts
func (c *Client) ReloadAllScripts(ctx context.Context) (map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "POST", "/scripts/reload-all", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := c.decodeResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// GetMetrics gets service metrics
func (c *Client) GetMetrics(ctx context.Context) (*MetricsResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/metrics", nil)
	if err != nil {
		return nil, err
	}

	var metricsResp MetricsResponse
	if err := c.decodeResponse(resp, &metricsResp); err != nil {
		return nil, err
	}

	return &metricsResp, nil
}

// ==================== BATCH OPERATIONS ====================

// BatchExecuteScript executes multiple scripts concurrently
func (c *Client) BatchExecuteScript(ctx context.Context, requests []ScriptExecuteRequest) ([]ScriptExecuteResponse, error) {
	// Create a worker pool
	type job struct {
		index int
		req   ScriptExecuteRequest
	}

	type result struct {
		index int
		resp  ScriptExecuteResponse
		err   error
	}

	numWorkers := len(requests)
	if numWorkers > 20 {
		numWorkers = 20 // Limit to 20 concurrent workers
	}

	jobs := make(chan job, len(requests))
	results := make(chan result, len(requests))

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range jobs {
				resp, err := c.ExecuteScript(ctx, j.req)
				results <- result{
					index: j.index,
					resp:  *resp,
					err:   err,
				}
			}
		}(i)
	}

	// Send jobs
	for i, req := range requests {
		jobs <- job{
			index: i,
			req:   req,
		}
	}
	close(jobs)

	// Wait for all workers to finish
	wg.Wait()
	close(results)

	// Collect results
	responses := make([]ScriptExecuteResponse, len(requests))
	errors := make([]error, 0)

	for r := range results {
		if r.err != nil {
			errors = append(errors, fmt.Errorf("request %d failed: %w", r.index, r.err))
		}
		responses[r.index] = r.resp
	}

	// Return results with any errors
	if len(errors) > 0 {
		return responses, fmt.Errorf("batch execution completed with %d errors, first error: %w", len(errors), errors[0])
	}

	return responses, nil
}

// ==================== CLIENT MANAGEMENT ====================

// UpdateConfig updates client configuration (thread-safe)
func (c *Client) UpdateConfig(newConfig Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Validate new configuration
	if newConfig.BaseURL == "" {
		return fmt.Errorf("base URL is required")
	}

	// Update configuration
	c.config = newConfig

	// Update HTTP client
	c.httpClient.Transport = &http.Transport{
		MaxIdleConns:        newConfig.MaxIdleConns,
		MaxIdleConnsPerHost: newConfig.MaxIdleConnsPerHost,
		IdleConnTimeout:     newConfig.IdleConnTimeout,
	}

	c.httpClient.Timeout = newConfig.RequestTimeout

	// Update rate limiter
	if newConfig.RateLimitPerSecond > 0 {
		c.rateLimiter.SetLimit(rate.Limit(newConfig.RateLimitPerSecond))
		c.rateLimiter.SetBurst(newConfig.RateBurst)
	} else {
		c.rateLimiter.SetLimit(rate.Inf)
		c.rateLimiter.SetBurst(1)
	}

	// Update or recreate circuit breaker if needed
	if c.circuitBreaker != nil && !newConfig.EnableCircuitBreaker {
		c.circuitBreaker = nil
	} else if c.circuitBreaker == nil && newConfig.EnableCircuitBreaker {
		c.circuitBreaker = NewCircuitBreaker(newConfig.CircuitFailureThreshold, newConfig.CircuitResetTimeout)
	}

	// Update metrics collector
	if c.metrics != nil && !newConfig.EnableMetrics {
		c.metrics = nil
	} else if c.metrics == nil && newConfig.EnableMetrics {
		c.metrics = NewMetricsCollector()
	}

	return nil
}

// GetClientMetrics gets client-side metrics
func (c *Client) GetClientMetrics() map[string]interface{} {
	if c.metrics == nil {
		return map[string]interface{}{
			"metrics_enabled": false,
		}
	}

	metrics := c.metrics.GetMetrics()
	metrics["rate_limit_per_second"] = c.config.RateLimitPerSecond
	metrics["rate_burst"] = c.config.RateBurst

	if c.circuitBreaker != nil {
		metrics["circuit_breaker_state"] = c.circuitBreaker.State()
		metrics["circuit_breaker_enabled"] = true
	} else {
		metrics["circuit_breaker_enabled"] = false
	}

	metrics["config"] = map[string]interface{}{
		"base_url":                c.config.BaseURL,
		"max_retries":             c.config.MaxRetries,
		"request_timeout_seconds": c.config.RequestTimeout.Seconds(),
		"max_idle_conns":          c.config.MaxIdleConns,
	}

	return metrics
}

// GetConfig returns the current configuration
func (c *Client) GetConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	// Currently nothing special to close
	// HTTP client will be garbage collected
	return nil
}

// ==================== CONVENIENCE FUNCTIONS ====================

// CreateExecuteRequest creates a script execute request
func CreateExecuteRequest(scriptName, functionName string, args []interface{}, kwargs map[string]interface{}) ScriptExecuteRequest {
	return ScriptExecuteRequest{
		ScriptName:   scriptName,
		FunctionName: functionName,
		Args:         args,
		Kwargs:       kwargs,
	}
}

// CreateExecuteRequestWithPath creates a script execute request with script path
func CreateExecuteRequestWithPath(scriptPath, functionName string, args []interface{}, kwargs map[string]interface{}) ScriptExecuteRequest {
	return ScriptExecuteRequest{
		ScriptPath:   scriptPath,
		FunctionName: functionName,
		Args:         args,
		Kwargs:       kwargs,
	}
}
