//go:build cgo
// +build cgo

package wasmplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestIsWASMEnabled tests the IsWASMEnabled function
func TestIsWASMEnabled(t *testing.T) {
	// Save original value
	originalValue := os.Getenv("WASM_ENABLE")
	defer os.Setenv("WASM_ENABLE", originalValue)

	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"enabled with true", "true", true},
		{"enabled with TRUE", "TRUE", true},
		{"enabled with True", "True", true},
		{"disabled with false", "false", false},
		{"disabled with empty", "", false},
		{"disabled with other", "other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("WASM_ENABLE", tt.envValue)
			result := IsWASMEnabled()
			if result != tt.expected {
				t.Errorf("IsWASMEnabled() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestNewManager tests the NewManager function
func TestNewManager(t *testing.T) {
	// Save original value
	originalValue := os.Getenv("WASM_ENABLE")
	defer os.Setenv("WASM_ENABLE", originalValue)

	t.Run("WASM disabled", func(t *testing.T) {
		os.Setenv("WASM_ENABLE", "false")
		_, err := NewManager(Config{})
		if err == nil {
			t.Error("Expected error when WASM is disabled")
		}
		if err.Error() != "WASM functionality is disabled. Set WASM_ENABLE=true to enable." {
			t.Errorf("Unexpected error message: %v", err)
		}
	})

	t.Run("WASM enabled", func(t *testing.T) {
		os.Setenv("WASM_ENABLE", "true")
		tempDir := t.TempDir()

		config := Config{
			WatchDir:     tempDir,
			AutoReload:   false,
			MaxInstances: 2,
			HealthCheck:  0, // Disable health check for faster tests
		}

		mgr, err := NewManager(config)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		if mgr == nil {
			t.Fatal("NewManager() returned nil manager")
		}

		// Cleanup
		mgr.Shutdown()
	})

	t.Run("default config values", func(t *testing.T) {
		os.Setenv("WASM_ENABLE", "true")
		tempDir := t.TempDir()

		config := Config{
			WatchDir:     tempDir,
			MaxInstances: 0, // Should use default
			HealthCheck:  0,
		}

		mgr, err := NewManager(config)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}

		if mgr.config.MaxInstances != DefaultConfig.MaxInstances {
			t.Errorf("Expected MaxInstances = %d, got %d", DefaultConfig.MaxInstances, mgr.config.MaxInstances)
		}

		mgr.Shutdown()
	})

	t.Run("empty WatchDir uses default", func(t *testing.T) {
		os.Setenv("WASM_ENABLE", "true")
		config := Config{
			WatchDir:     "",
			MaxInstances: 2,
			HealthCheck:  0,
		}

		mgr, err := NewManager(config)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}

		if mgr.config.WatchDir == "" {
			t.Error("Expected WatchDir to be set to default value")
		}

		mgr.Shutdown()
	})
}

// TestLoadModule tests the LoadModule function
func TestLoadModule(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("non-existent file", func(t *testing.T) {
		err := mgr.LoadModule("nonexistent.wasm")
		if err != nil {
			t.Errorf("LoadModule() with non-existent file should not error, got: %v", err)
		}
	})

	t.Run("path security check", func(t *testing.T) {
		// Set restricted allowed paths
		mgr.config.AllowedPaths = []string{tempDir}

		// Try to load a file outside allowed path
		err := mgr.LoadModule("../outside.wasm")
		if err == nil {
			t.Error("Expected error for path outside allowed paths")
		}
		if err.Error() != "path not allowed: ../outside.wasm" {
			t.Errorf("Unexpected error message: %v", err)
		}
	})

	t.Run("empty allowed paths allows all", func(t *testing.T) {
		mgr.config.AllowedPaths = []string{}
		err := mgr.LoadModule("anyfile.wasm")
		// Should not error due to path check (will error due to missing file, but that's OK)
		if err != nil && err.Error() == "path not allowed: anyfile.wasm" {
			t.Error("Expected empty allowed paths to allow all paths")
		}
	})
}

// TestCallFunction tests the CallFunction method
func TestCallFunction(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("module not found", func(t *testing.T) {
		result, err := mgr.CallFunction("nonexistent", "func", nil)
		if err == nil {
			t.Error("Expected error for non-existent module")
		}
		if result.Success {
			t.Error("Expected result.Success to be false")
		}
		if result.Error == "" {
			t.Error("Expected error message in result")
		}
	})

	t.Run("empty instance pool", func(t *testing.T) {
		// Create an empty module entry
		mgr.moduleStore.Store("empty", []*WasmInstance{})
		lock, _ := mgr.moduleLocks.LoadOrStore("empty", &sync.RWMutex{})
		mu := lock.(*sync.RWMutex)
		mu.Lock()
		defer mu.Unlock()

		result, err := mgr.CallFunction("empty", "func", nil)
		if err == nil {
			t.Error("Expected error for empty instance pool")
		}
		if result.Success {
			t.Error("Expected result.Success to be false")
		}
	})
}

// TestGetModuleInfo tests the GetModuleInfo method
func TestGetModuleInfo(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("module not found", func(t *testing.T) {
		info, exists := mgr.GetModuleInfo("nonexistent")
		if exists {
			t.Error("Expected module to not exist")
		}
		if info.Name != "" {
			t.Error("Expected empty ModuleInfo for non-existent module")
		}
	})
}

// TestListModules tests the ListModules method
func TestListModules(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("empty list", func(t *testing.T) {
		modules := mgr.ListModules()
		if len(modules) != 0 {
			t.Errorf("Expected empty list, got %d modules", len(modules))
		}
	})
}

// TestUnloadModule tests the UnloadModule method
func TestUnloadModule(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("unload non-existent module", func(t *testing.T) {
		// Should not panic
		mgr.UnloadModule("nonexistent")
	})
}

// TestShutdown tests the Shutdown method
func TestShutdown(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	t.Run("shutdown without modules", func(t *testing.T) {
		mgr.Shutdown()
		// Should not panic
	})

	t.Run("multiple shutdown calls", func(t *testing.T) {
		mgr2, err := NewManager(config)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		mgr2.Shutdown()
		mgr2.Shutdown() // Should be safe to call multiple times
	})
}

// TestCallbacks tests the callback functionality
func TestCallbacks(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("module loaded callback", func(t *testing.T) {
		var called bool
		var moduleName string
		mgr.OnModuleLoaded(func(name string) {
			called = true
			moduleName = name
		})

		// Trigger callback manually (since we can't easily load a real WASM file)
		if mgr.onModuleLoaded != nil {
			mgr.onModuleLoaded("test.wasm")
		}

		if !called {
			t.Error("Expected callback to be called")
		}
		if moduleName != "test.wasm" {
			t.Errorf("Expected module name 'test.wasm', got '%s'", moduleName)
		}
	})

	t.Run("module unloaded callback", func(t *testing.T) {
		var called bool
		var moduleName string
		mgr.OnModuleUnloaded(func(name string) {
			called = true
			moduleName = name
		})

		// Trigger callback manually
		if mgr.onModuleUnloaded != nil {
			mgr.onModuleUnloaded("test.wasm")
		}

		if !called {
			t.Error("Expected callback to be called")
		}
		if moduleName != "test.wasm" {
			t.Errorf("Expected module name 'test.wasm', got '%s'", moduleName)
		}
	})

	t.Run("call error callback", func(t *testing.T) {
		var called bool
		var modName, funcName string
		var callbackErr error
		mgr.OnCallError(func(module, function string, err error) {
			called = true
			modName = module
			funcName = function
			callbackErr = err
		})

		// Trigger callback manually
		if mgr.onCallError != nil {
			mgr.onCallError("test.wasm", "testFunc", fmt.Errorf("test error"))
		}

		if !called {
			t.Error("Expected callback to be called")
		}
		if modName != "test.wasm" {
			t.Errorf("Expected module name 'test.wasm', got '%s'", modName)
		}
		if funcName != "testFunc" {
			t.Errorf("Expected function name 'testFunc', got '%s'", funcName)
		}
		if callbackErr == nil {
			t.Error("Expected error to be passed to callback")
		}
	})
}

// TestPathSecurity tests the path security check
func TestPathSecurity(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
		AllowedPaths: []string{tempDir},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("allowed path", func(t *testing.T) {
		allowed := mgr.isPathAllowed("test.wasm")
		if !allowed {
			t.Error("Expected path to be allowed")
		}
	})

	t.Run("disallowed path", func(t *testing.T) {
		allowed := mgr.isPathAllowed("../outside.wasm")
		if allowed {
			t.Error("Expected path to be disallowed")
		}
	})

	t.Run("empty allowed paths allows all", func(t *testing.T) {
		mgr.config.AllowedPaths = []string{}
		allowed := mgr.isPathAllowed("anyfile.wasm")
		if !allowed {
			t.Error("Expected empty allowed paths to allow all")
		}
	})
}

// TestConcurrentAccess tests concurrent access to the manager
func TestConcurrentAccess(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	t.Run("concurrent ListModules", func(t *testing.T) {
		var wg sync.WaitGroup
		iterations := 10
		goroutines := 5

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					_ = mgr.ListModules()
				}
			}()
		}

		wg.Wait()
		// Should not panic
	})

	t.Run("concurrent GetModuleInfo", func(t *testing.T) {
		var wg sync.WaitGroup
		iterations := 10
		goroutines := 5

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					_, _ = mgr.GetModuleInfo("test")
				}
			}(i)
		}

		wg.Wait()
		// Should not panic
	})
}

// TestGetEnvOrDefault tests the helper function
func TestGetEnvOrDefault(t *testing.T) {
	// Save original value
	originalValue := os.Getenv("TEST_ENV_VAR")
	defer os.Setenv("TEST_ENV_VAR", originalValue)

	t.Run("environment variable set", func(t *testing.T) {
		os.Setenv("TEST_ENV_VAR", "test_value")
		result := getEnvOrDefault("TEST_ENV_VAR", "default")
		if result != "test_value" {
			t.Errorf("Expected 'test_value', got '%s'", result)
		}
	})

	t.Run("environment variable not set", func(t *testing.T) {
		os.Unsetenv("TEST_ENV_VAR")
		result := getEnvOrDefault("TEST_ENV_VAR", "default")
		if result != "default" {
			t.Errorf("Expected 'default', got '%s'", result)
		}
	})

	t.Run("environment variable empty", func(t *testing.T) {
		os.Setenv("TEST_ENV_VAR", "")
		result := getEnvOrDefault("TEST_ENV_VAR", "default")
		if result != "default" {
			t.Errorf("Expected 'default', got '%s'", result)
		}
	})
}

// TestConfigDefaults tests default configuration values
func TestConfigDefaults(t *testing.T) {
	if DefaultConfig.MaxInstances <= 0 {
		t.Error("DefaultConfig.MaxInstances should be greater than 0")
	}

	if DefaultConfig.HealthCheck <= 0 {
		t.Error("DefaultConfig.HealthCheck should be greater than 0")
	}

	if DefaultConfig.WatchDir == "" {
		t.Error("DefaultConfig.WatchDir should not be empty")
	}
}

// TestFileWatcher tests file watcher functionality (if enabled)
func TestFileWatcher(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   true,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	// Give watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Test that watcher was initialized (or gracefully failed)
	// The actual file watching is hard to test without real file system events
	// but we can at least verify the manager was created successfully
	if mgr == nil {
		t.Fatal("Manager should be created")
	}
}

// TestHealthCheck tests health check functionality
func TestHealthCheck(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  100 * time.Millisecond, // Short interval for testing
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	// Give health check loop time to start
	time.Sleep(50 * time.Millisecond)

	// Health check loop should be running
	// We can't easily test the actual health check logic without real instances,
	// but we can verify the manager was created with health check enabled
	if mgr.config.HealthCheck == 0 {
		t.Error("Health check should be enabled")
	}
}

// TestPreloadModules tests preload functionality
func TestPreloadModules(t *testing.T) {
	if !IsWASMEnabled() {
		t.Skip("WASM_ENABLE is not set to true, skipping test")
	}

	tempDir := t.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	// Create a dummy .wasm file (not a real WASM file, but tests the preload logic)
	dummyFile := filepath.Join(tempDir, "dummy.wasm")
	if err := os.WriteFile(dummyFile, []byte("dummy"), 0644); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	// Preload should have attempted to load the file
	// It will fail because it's not a real WASM file, but that's OK for this test
	// We're just testing that preload runs without panicking
}

// BenchmarkListModules benchmarks the ListModules method
func BenchmarkListModules(b *testing.B) {
	if !IsWASMEnabled() {
		b.Skip("WASM_ENABLE is not set to true, skipping benchmark")
	}

	tempDir := b.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		b.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.ListModules()
	}
}

// BenchmarkGetModuleInfo benchmarks the GetModuleInfo method
func BenchmarkGetModuleInfo(b *testing.B) {
	if !IsWASMEnabled() {
		b.Skip("WASM_ENABLE is not set to true, skipping benchmark")
	}

	tempDir := b.TempDir()
	config := Config{
		WatchDir:     tempDir,
		AutoReload:   false,
		MaxInstances: 2,
		HealthCheck:  0,
	}

	mgr, err := NewManager(config)
	if err != nil {
		b.Fatalf("NewManager() error = %v", err)
	}
	defer mgr.Shutdown()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.GetModuleInfo("test")
	}
}

