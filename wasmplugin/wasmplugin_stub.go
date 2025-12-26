//go:build !cgo
// +build !cgo

package wasmplugin

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the configuration for WasmManager
type Config struct {
	WatchDir     string
	AutoReload   bool
	MaxInstances int
	HealthCheck  time.Duration
	AllowedPaths []string
}

// DefaultConfig provides default configuration values
var DefaultConfig = Config{
	WatchDir:     "./functions",
	AutoReload:   true,
	MaxInstances: 5,
	HealthCheck:  5 * time.Minute,
	AllowedPaths: []string{"./functions"},
}

// WasmManager is a stub when CGO is disabled
type WasmManager struct{}

// ModuleInfo provides module information
type ModuleInfo struct {
	Name          string
	InstanceCount int
	LoadedAt      time.Time
	TotalCalls    int64
	ExportedFuncs []string
}

// CallResult provides the result of a function call
type CallResult struct {
	Success  bool
	Results  []interface{}
	Duration time.Duration
	Error    string
	Stdout   string
	Stderr   string
}

// IsWASMEnabled checks the environment variable, but WASM won't work without CGO
func IsWASMEnabled() bool {
	env := os.Getenv("WASM_ENABLE")
	return strings.ToLower(env) == "true"
}

// NewManager returns an error when CGO is disabled
func NewManager(config Config) (*WasmManager, error) {
	// Check if WASM is enabled via environment variable
	if !IsWASMEnabled() {
		return nil, fmt.Errorf("WASM functionality is disabled. Set WASM_ENABLE=true to enable.")
	}
	// Even if enabled, CGO is required
	return nil, fmt.Errorf("WASM functionality requires CGO to be enabled at build time. Rebuild with CGO_ENABLED=1 to enable WASM support.")
}

// LoadModule is a stub that returns an error
func (m *WasmManager) LoadModule(filename string) error {
	return fmt.Errorf("WASM functionality requires CGO to be enabled")
}

// CallFunction is a stub that returns an error
func (m *WasmManager) CallFunction(moduleName, funcName string, params []interface{}) (CallResult, error) {
	return CallResult{
		Success: false,
		Error:   "WASM functionality requires CGO to be enabled",
	}, fmt.Errorf("WASM functionality requires CGO to be enabled")
}

// IsModuleLoaded is a stub
func (m *WasmManager) IsModuleLoaded(moduleName string) bool {
	return false
}

// EnsureModuleLoaded is a stub
func (m *WasmManager) EnsureModuleLoaded(filename string) error {
	return fmt.Errorf("WASM functionality requires CGO to be enabled")
}

// GetModuleInfo is a stub
func (m *WasmManager) GetModuleInfo(moduleName string) (ModuleInfo, bool) {
	return ModuleInfo{}, false
}

// ListModules is a stub
func (m *WasmManager) ListModules() []ModuleInfo {
	return []ModuleInfo{}
}

// UnloadModule is a stub
func (m *WasmManager) UnloadModule(filename string) {
	// No-op
}

// Shutdown is a stub
func (m *WasmManager) Shutdown() {
	// No-op
}

// OnModuleLoaded sets the callback for module loaded events
func (m *WasmManager) OnModuleLoaded(fn func(string)) {
	// No-op
}

// OnModuleUnloaded sets the callback for module unloaded events
func (m *WasmManager) OnModuleUnloaded(fn func(string)) {
	// No-op
}

// OnCallError sets the callback for call error events
func (m *WasmManager) OnCallError(fn func(string, string, error)) {
	// No-op
}
