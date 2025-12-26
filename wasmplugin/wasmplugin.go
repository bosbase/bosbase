//go:build cgo
// +build cgo

package wasmplugin

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/second-state/WasmEdge-go/wasmedge"
	bindgen "github.com/second-state/wasmedge-bindgen/host/go"
)

// ==============================
// Configuration Structures
// ==============================
type Config struct {
	WatchDir     string        `json:"watch_dir" yaml:"watch_dir"`
	AutoReload   bool          `json:"auto_reload" yaml:"auto_reload"`
	MaxInstances int           `json:"max_instances" yaml:"max_instances"`
	HealthCheck  time.Duration `json:"health_check" yaml:"health_check"`
	AllowedPaths []string      `json:"allowed_paths" yaml:"allowed_paths"`
}

// Default configuration
var DefaultConfig = Config{
	WatchDir:     getEnvOrDefault("EXECUTE_PATH", "./functions"),
	AutoReload:   true,
	MaxInstances: getEnvIntOrDefault("WASM_INSTANCE_NUM", 5),
	HealthCheck:  5 * time.Minute,
	AllowedPaths: []string{getEnvOrDefault("EXECUTE_PATH", "./functions")},
}

// Helper function to get environment variable or return default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Helper function to get integer environment variable or return default
func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var result int
		if _, err := fmt.Sscanf(value, "%d", &result); err == nil && result > 0 {
			return result
		}
	}
	return defaultValue
}

// ==============================
// WASM Instance
// ==============================
type WasmInstance struct {
	VM         *wasmedge.VM
	BindGen    *bindgen.Bindgen
	LoadedAt   time.Time
	UsageCount int64 // Use atomic operations for thread-safe access
	LastUsed   int64 // Unix timestamp (atomic access)
	Healthy    int32 // Use atomic for thread-safe boolean (0=false, 1=true)
	// Mutex to protect VM/BindGen execution (WasmEdge VM may not be thread-safe)
	execMutex sync.Mutex
	// Buffers for capturing stdout/stderr
	StdoutBuf *bytes.Buffer
	StderrBuf *bytes.Buffer
	// Writers for WASI
	StdoutWriter io.Writer
	StderrWriter io.Writer
}

// ==============================
// Module Information
// ==============================
type ModuleInfo struct {
	Name          string
	InstanceCount int
	LoadedAt      time.Time
	TotalCalls    int64
	ExportedFuncs []string
}

// ==============================
// Call Result
// ==============================
type CallResult struct {
	Success  bool
	Results  []interface{}
	Duration time.Duration
	Error    string
	Stdout   string // Captured stdout output
	Stderr   string // Captured stderr output
}

// ==============================
// Core Manager
// ==============================
type WasmManager struct {
	config        Config
	runtimeConfig *wasmedge.Configure

	// Core storage
	moduleStore sync.Map // key: module name -> []*WasmInstance
	moduleLocks sync.Map // key: module name -> *sync.RWMutex
	moduleInfo  sync.Map // key: module name -> *ModuleInfo

	// File watcher
	watcher     *fsnotify.Watcher
	watcherStop chan struct{}
	watcherDone sync.WaitGroup

	// Shutdown control
	shutdownOnce sync.Once

	// Statistics
	stats struct {
		sync.RWMutex
		totalLoads  int64
		totalCalls  int64
		failedLoads int64
		failedCalls int64
	}

	// Mutex for stdout/stderr redirection (thread-safe capture)
	stdoutStderrMutex sync.Mutex

	// Callback functions
	onModuleLoaded   func(string)                // Module loaded callback
	onModuleUnloaded func(string)                // Module unloaded callback
	onCallError      func(string, string, error) // Call error callback
}

// ==============================
// Check if WASM is enabled
// ==============================
func IsWASMEnabled() bool {
	env := os.Getenv("WASM_ENABLE")
	return strings.ToLower(env) == "true"
}

// ==============================
// Create Manager
// ==============================
func NewManager(config Config) (*WasmManager, error) {
	// Check if WASM is enabled
	if !IsWASMEnabled() {
		return nil, fmt.Errorf("WASM functionality is disabled. Set WASM_ENABLE=true to enable.")
	}

	if config.MaxInstances <= 0 {
		config.MaxInstances = DefaultConfig.MaxInstances
	}
	if config.WatchDir == "" {
		config.WatchDir = DefaultConfig.WatchDir
	}

	// Create runtime configuration
	conf := wasmedge.NewConfigure(
		wasmedge.WASI,
		wasmedge.REFERENCE_TYPES,
		wasmedge.BULK_MEMORY_OPERATIONS,
	)

	// Create manager
	mgr := &WasmManager{
		config:        config,
		runtimeConfig: conf,
		watcherStop:   make(chan struct{}),
	}

	// Ensure directory exists
	if err := os.MkdirAll(config.WatchDir, 0755); err != nil {
		conf.Release()
		return nil, fmt.Errorf("failed to create directory: %v", err)
	}

	// Initialize file watcher
	if config.AutoReload {
		if err := mgr.initWatcher(); err != nil {
			// Continue without watcher if initialization fails
		}
	}

	// Preload existing modules
	mgr.preloadModules()

	// Start health check loop
	if config.HealthCheck > 0 {
		go mgr.healthCheckLoop()
	}

	return mgr, nil
}

// ==============================
// Core API: Load Module
// ==============================
func (m *WasmManager) LoadModule(filename string) error {
	// Security check
	if !m.isPathAllowed(filename) {
		return fmt.Errorf("path not allowed: %s", filename)
	}

	wasmPath := filepath.Join(m.config.WatchDir, filename)

	// Check if file exists
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		// File doesn't exist, unload module
		m.UnloadModule(filename)
		return fmt.Errorf("WASM file not found at %s. Make sure the file is copied to the EXECUTE_PATH directory (%s)", wasmPath, m.config.WatchDir)
	}

	// Get or create module lock
	lock, _ := m.moduleLocks.LoadOrStore(filename, &sync.RWMutex{})
	mu := lock.(*sync.RWMutex)
	mu.Lock()
	defer mu.Unlock()

	// Release old instances
	if oldInstances, exists := m.moduleStore.Load(filename); exists {
		instances := oldInstances.([]*WasmInstance)
		for _, inst := range instances {
			m.releaseInstance(inst)
		}
	}

	// Create new instance pool
	instances := make([]*WasmInstance, 0, m.config.MaxInstances)
	var exportedFuncs []string

	for i := 0; i < m.config.MaxInstances; i++ {
		instance, funcs, err := m.createInstance(wasmPath)
		if err != nil {
			// Clean up already created instances
			for _, inst := range instances {
				m.releaseInstance(inst)
			}
			return fmt.Errorf("failed to create instance: %v", err)
		}
		instances = append(instances, instance)

		// Collect exported functions (only from first instance)
		if i == 0 {
			exportedFuncs = funcs
		}
	}

	// Store instance pool
	m.moduleStore.Store(filename, instances)

	// Update module information
	m.moduleInfo.Store(filename, &ModuleInfo{
		Name:          filename,
		InstanceCount: len(instances),
		LoadedAt:      time.Now(),
		ExportedFuncs: exportedFuncs,
	})

	// Update statistics
	m.stats.Lock()
	m.stats.totalLoads++
	m.stats.Unlock()

	// Callback
	if m.onModuleLoaded != nil {
		m.onModuleLoaded(filename)
	}

	return nil
}

// ==============================
// Core API: Call Function
// ==============================
// Concurrency safety:
// - Module-level RLock: Prevents LoadModule from modifying instances array during execution
// - Instance-level execMutex: Serializes VM execution (WasmEdge VM is NOT thread-safe)
// - Atomic operations: Thread-safe access to UsageCount, LastUsed, Healthy fields
// - Result isolation: Each call gets its own results slice, preventing result mixing
func (m *WasmManager) CallFunction(moduleName, funcName string, params []interface{}) (CallResult, error) {
	startTime := time.Now()

	// Get module lock (read lock) - prevents LoadModule from modifying instances array
	lockRaw, ok := m.moduleLocks.Load(moduleName)
	if !ok {
		return CallResult{
			Success: false,
			Error:   fmt.Sprintf("module not found: %s", moduleName),
		}, fmt.Errorf("module not found: %s", moduleName)
	}

	mu := lockRaw.(*sync.RWMutex)
	mu.RLock()
	defer mu.RUnlock()

	// Get instance pool under the module lock to avoid races with reloads
	instancesRaw, exists := m.moduleStore.Load(moduleName)
	if !exists {
		return CallResult{
			Success: false,
			Error:   fmt.Sprintf("module not found: %s", moduleName),
		}, fmt.Errorf("module not found: %s", moduleName)
	}

	instances := instancesRaw.([]*WasmInstance)
	if len(instances) == 0 {
		return CallResult{
			Success: false,
			Error:   fmt.Sprintf("module instance pool is empty: %s", moduleName),
		}, fmt.Errorf("module instance pool is empty: %s", moduleName)
	}

	// Select instance (simple round-robin)
	m.stats.Lock()
	idx := m.stats.totalCalls % int64(len(instances))
	m.stats.totalCalls++
	m.stats.Unlock()

	instance := instances[idx]

	// Update instance statistics atomically (thread-safe)
	atomic.AddInt64(&instance.UsageCount, 1)
	atomic.StoreInt64(&instance.LastUsed, time.Now().UnixNano())

	// Execute function and get return value
	// CRITICAL: Lock the instance to prevent concurrent execution on the same VM
	// WasmEdge VM instances are NOT thread-safe - each instance must execute serially
	instance.execMutex.Lock()
	var results []interface{}
	var execErr error

	// For _start, we need to reload and re-instantiate the module to execute it
	// _start is the WASM entry point that runs during instantiation
	// For simple Rust programs with main(), there's no exported "main" function either
	if funcName == "_start" {
		// Use the existing VM instance but reload the module to trigger _start
		// This is simpler than creating a new VM
		wasmPath := filepath.Join(m.config.WatchDir, moduleName)
		execErr = m.executeStartWithExistingVM(instance.VM, wasmPath)
		if execErr == nil {
			results = []interface{}{}
		}
	} else {
		// Use BindGen Execute to call the function - following WasmEdge bindgen pattern
		// Pattern: res, _, err = bg.Execute("function_name", params...)
		// The second return value is ignored (bindgen internal state)
		results, _, execErr = instance.BindGen.Execute(funcName, params...)
		if execErr != nil {
			// Improve error message with helpful debugging info
			errMsg := execErr.Error()
			if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "function") {
				execErr = fmt.Errorf("function '%s' not found. Make sure: 1) The WASM file was recompiled with 'cargo build --target wasm32-wasip1 --release' after code changes, 2) The function is exported using #[wasmedge_bindgen] macro, 3) The WASM file is in the EXECUTE_PATH directory. Original error: %v", funcName, execErr)
			}
		}
	}
	instance.execMutex.Unlock()

	duration := time.Since(startTime)

	// Convert WASM function return value to string as output
	var outputBuilder strings.Builder
	for i, result := range results {
		if i > 0 {
			outputBuilder.WriteString(" ")
		}
		outputBuilder.WriteString(fmt.Sprintf("%v", result))
	}
	outputString := outputBuilder.String()

	if execErr != nil {
		m.stats.Lock()
		m.stats.failedCalls++
		m.stats.Unlock()

		// Mark instance as unhealthy (thread-safe)
		atomic.StoreInt32(&instance.Healthy, 0)

		// Error callback
		if m.onCallError != nil {
			m.onCallError(moduleName, funcName, execErr)
		}

		return CallResult{
			Success:  false,
			Duration: duration,
			Error:    execErr.Error(),
			Stdout:   "", // Not using stdout for output
			Stderr:   "", // Not using stderr for output
		}, execErr
	}

	// Mark instance as healthy (thread-safe)
	atomic.StoreInt32(&instance.Healthy, 1)

	return CallResult{
		Success:  true,
		Results:  results,
		Duration: duration,
		Stdout:   outputString, // Use WASM return value as output
		Stderr:   "",           // Not using stderr for output
	}, nil
}

// executeStartWithExistingVM uses an existing VM instance to reload and execute _start
// This is simpler than creating a new VM - we just reload the module which triggers _start
// IMPORTANT: File descriptors 1 and 2 MUST be redirected to temp files BEFORE calling this function
func (m *WasmManager) executeStartWithExistingVM(vm *wasmedge.VM, wasmPath string) error {
	// The VM already has WASI initialized, so we just need to reload the module
	// Load WASM file from disk
	if loadErr := vm.LoadWasmFile(wasmPath); loadErr != nil {
		return fmt.Errorf("failed to load WASM file: %v", loadErr)
	}

	// Validate the WASM module
	if validateErr := vm.Validate(); validateErr != nil {
		return fmt.Errorf("failed to validate WASM: %v", validateErr)
	}

	// Instantiate the WASM module
	// This automatically executes the _start function (which calls main() in Rust programs)
	// Output goes to original stdout/stderr (not captured)
	if instantiateErr := vm.Instantiate(); instantiateErr != nil {
		return fmt.Errorf("failed to instantiate WASM (which executes _start): %v", instantiateErr)
	}

	return nil
}

// ==============================
// Core API: Check if Module is Loaded
// ==============================
func (m *WasmManager) IsModuleLoaded(moduleName string) bool {
	_, exists := m.moduleStore.Load(moduleName)
	return exists
}

// ==============================
// Core API: Ensure Module is Loaded (load only if not already loaded)
// ==============================
func (m *WasmManager) EnsureModuleLoaded(filename string) error {
	if m.IsModuleLoaded(filename) {
		return nil
	}
	return m.LoadModule(filename)
}

// ==============================
// Core API: Get Module Information
// ==============================
func (m *WasmManager) GetModuleInfo(moduleName string) (ModuleInfo, bool) {
	infoRaw, exists := m.moduleInfo.Load(moduleName)
	if !exists {
		return ModuleInfo{}, false
	}

	info := infoRaw.(*ModuleInfo)

	// Get real-time call statistics (thread-safe reads)
	if instancesRaw, ok := m.moduleStore.Load(moduleName); ok {
		instances := instancesRaw.([]*WasmInstance)
		var totalCalls int64
		for _, inst := range instances {
			totalCalls += atomic.LoadInt64(&inst.UsageCount)
		}
		info.TotalCalls = totalCalls
	}

	return *info, true
}

// ==============================
// Core API: List All Modules
// ==============================
func (m *WasmManager) ListModules() []ModuleInfo {
	var modules []ModuleInfo

	m.moduleInfo.Range(func(key, value interface{}) bool {
		info := value.(*ModuleInfo)

		// Update call statistics (thread-safe reads)
		if instancesRaw, ok := m.moduleStore.Load(key.(string)); ok {
			instances := instancesRaw.([]*WasmInstance)
			var totalCalls int64
			for _, inst := range instances {
				totalCalls += atomic.LoadInt64(&inst.UsageCount)
			}
			info.TotalCalls = totalCalls
		}

		modules = append(modules, *info)
		return true
	})

	return modules
}

// ==============================
// Core API: Unload Module
// ==============================
func (m *WasmManager) UnloadModule(filename string) {
	// Get module lock
	if lockRaw, exists := m.moduleLocks.Load(filename); exists {
		mu := lockRaw.(*sync.RWMutex)
		mu.Lock()
		defer mu.Unlock()

		// Release all instances
		if instancesRaw, ok := m.moduleStore.Load(filename); ok {
			instances := instancesRaw.([]*WasmInstance)
			for _, instance := range instances {
				m.releaseInstance(instance)
			}
		}

		// Clean up storage
		m.moduleStore.Delete(filename)
		m.moduleLocks.Delete(filename)
		m.moduleInfo.Delete(filename)

		// Callback
		if m.onModuleUnloaded != nil {
			m.onModuleUnloaded(filename)
		}
	}
}

// ==============================
// Core API: Shutdown Manager
// ==============================
func (m *WasmManager) Shutdown() {
	m.shutdownOnce.Do(func() {
		// Stop file watcher
		close(m.watcherStop)
		if m.watcher != nil {
			m.watcher.Close()
		}
		m.watcherDone.Wait()

		// Unload all modules
		m.moduleStore.Range(func(key, value interface{}) bool {
			m.UnloadModule(key.(string))
			return true
		})

		// Release runtime configuration
		if m.runtimeConfig != nil {
			m.runtimeConfig.Release()
		}
	})
}

// ==============================
// Callback Function Setters
// ==============================
func (m *WasmManager) OnModuleLoaded(fn func(string)) {
	m.onModuleLoaded = fn
}

func (m *WasmManager) OnModuleUnloaded(fn func(string)) {
	m.onModuleUnloaded = fn
}

func (m *WasmManager) OnCallError(fn func(string, string, error)) {
	m.onCallError = fn
}

// ==============================
// Internal Method: Create Instance
// ==============================
func (m *WasmManager) createInstance(wasmPath string) (*WasmInstance, []string, error) {
	// Create VM
	vm := wasmedge.NewVMWithConfig(m.runtimeConfig)
	if vm == nil {
		return nil, nil, fmt.Errorf("failed to create VM")
	}

	// Create buffers for capturing stdout/stderr
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	// Initialize WASI
	// Note: WasmEdge-go's WASI module writes to os.Stdout/os.Stderr by default
	// We need to intercept this. Since we can't easily override WASI's internal
	// file descriptors, we'll use a different approach: capture via host functions
	// or use the VM's execution context.
	// For now, we'll set up the buffers and try to capture output during execution
	wasi := vm.GetImportModule(wasmedge.WASI)
	wasi.InitWasi(nil, nil, []string{".:."})

	// Load WASM file
	if err := vm.LoadWasmFile(wasmPath); err != nil {
		vm.Release()
		return nil, nil, fmt.Errorf("failed to load WASM: %v", err)
	}

	// Validate module
	if err := vm.Validate(); err != nil {
		vm.Release()
		return nil, nil, fmt.Errorf("failed to validate WASM: %v", err)
	}

	// Create BindGen (must be created before instantiation)
	bg := bindgen.New(vm)
	if bg == nil {
		vm.Release()
		return nil, nil, fmt.Errorf("failed to create BindGen")
	}

	// Instantiate the bindgen and VM - this sets up helper functions like allocate/deallocate
	// required for wasmedge_bindgen macro functions
	// Note: bg.Instantiate() handles both VM instantiation and bindgen setup
	// Following the WasmEdge example pattern: bg.Instantiate() after LoadWasmFile and Validate
	// This injects the allocate/deallocate functions that #[wasmedge_bindgen] functions need
	bg.Instantiate()

	// Get exported function list
	var exportedFuncs []string
	// Function detection can be implemented here if needed

	return &WasmInstance{
		VM:           vm,
		BindGen:      bg,
		LoadedAt:     time.Now(),
		LastUsed:     time.Now().UnixNano(), // Store as UnixNano for atomic access
		Healthy:      1,                     // 1 = true for atomic boolean
		StdoutBuf:    stdoutBuf,
		StderrBuf:    stderrBuf,
		StdoutWriter: stdoutBuf,
		StderrWriter: stderrBuf,
	}, exportedFuncs, nil
}

// ==============================
// Internal Method: Release Instance
// ==============================
func (m *WasmManager) releaseInstance(instance *WasmInstance) {
	if instance == nil {
		return
	}

	if instance.VM != nil {
		instance.VM.Release()
		instance.VM = nil
	}

	// BindGen currently doesn't have a Release method
	instance.BindGen = nil
}

// ==============================
// Internal Method: Initialize File Watcher
// ==============================
func (m *WasmManager) initWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.watcher = watcher

	// Watch directory
	if err := watcher.Add(m.config.WatchDir); err != nil {
		return err
	}

	m.watcherDone.Add(1)
	go m.watchLoop()

	return nil
}

// ==============================
// Internal Method: File Watch Loop
// ==============================
func (m *WasmManager) watchLoop() {
	defer m.watcherDone.Done()

	debounce := time.NewTicker(500 * time.Millisecond)
	defer debounce.Stop()

	pendingReloads := make(map[string]bool)

	for {
		select {
		case <-m.watcherStop:
			return

		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			if strings.HasSuffix(event.Name, ".wasm") {
				filename := filepath.Base(event.Name)
				pendingReloads[filename] = true
			}

		case <-debounce.C:
			for filename := range pendingReloads {
				if err := m.LoadModule(filename); err != nil {
					// Error callback
					if m.onCallError != nil {
						m.onCallError("watch_loop", filename, err)
					}
				}
				delete(pendingReloads, filename)
			}

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			// Error callback
			if m.onCallError != nil {
				m.onCallError("watch_loop", "watcher_error", err)
			}
		}
	}
}

// ==============================
// Internal Method: Preload Modules
// ==============================
func (m *WasmManager) preloadModules() {
	entries, err := os.ReadDir(m.config.WatchDir)
	if err != nil {
		// Error callback
		if m.onCallError != nil {
			m.onCallError("preload", "read_dir", err)
		}
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".wasm") {
			if err := m.LoadModule(entry.Name()); err != nil {
				// Error callback
				if m.onCallError != nil {
					m.onCallError("preload", entry.Name(), err)
				}
			}
		}
	}
}

// ==============================
// Internal Method: Health Check Loop
// ==============================
func (m *WasmManager) healthCheckLoop() {
	ticker := time.NewTicker(m.config.HealthCheck)
	defer ticker.Stop()

	for {
		select {
		case <-m.watcherStop:
			return
		case <-ticker.C:
			m.doHealthCheck()
		}
	}
}

func (m *WasmManager) doHealthCheck() {
	m.moduleStore.Range(func(key, value interface{}) bool {
		moduleName := key.(string)
		instances := value.([]*WasmInstance)

		// Check instance health status (thread-safe reads)
		healthyCount := 0
		for _, instance := range instances {
			isHealthy := atomic.LoadInt32(&instance.Healthy) == 1
			lastUsedNano := atomic.LoadInt64(&instance.LastUsed)
			lastUsed := time.Unix(0, lastUsedNano)
			if isHealthy && time.Since(lastUsed) < m.config.HealthCheck*2 {
				healthyCount++
			}
		}

		// If too many unhealthy instances, reload module
		if healthyCount < len(instances)/2 {
			go func(name string) {
				if err := m.LoadModule(name); err != nil {
					// Error callback
					if m.onCallError != nil {
						m.onCallError("health_check", name, err)
					}
				}
			}(moduleName)
		}

		return true
	})
}

// ==============================
// Internal Method: Path Security Check
// ==============================
func (m *WasmManager) isPathAllowed(filename string) bool {
	fullPath := filepath.Join(m.config.WatchDir, filename)

	// Check if within allowed directories
	for _, allowed := range m.config.AllowedPaths {
		if strings.HasPrefix(fullPath, allowed) {
			return true
		}
	}

	// If no allowed paths specified, allow all
	if len(m.config.AllowedPaths) == 0 {
		return true
	}

	return false
}
