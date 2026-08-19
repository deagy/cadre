package knowledge

import (
	"sync"
)

// ConfigManager handles knowledge store configuration.
type ConfigManager struct {
	mu       sync.RWMutex
	config   map[string]interface{}
	defaults map[string]interface{}
}

// NewConfigManager creates a configuration manager with defaults.
func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		config: make(map[string]interface{}),
		defaults: map[string]interface{}{
			"backup_location":             "/backups",
			"backup_schedule_hours":       24,
			"replication_consistency":     "eventual",
			"fault_tolerance_max_retries": 3,
			"circuit_breaker_threshold":   5,
			"circuit_breaker_reset_sec":   30,
			"max_replication_lag_ms":      1000,
			"enable_metrics":              true,
			"metrics_retention_days":      30,
			"enable_diagnostics":          true,
		},
	}
}

// Get retrieves a configuration value.
func (cm *ConfigManager) Get(key string) (interface{}, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if val, ok := cm.config[key]; ok {
		return val, true
	}

	if val, ok := cm.defaults[key]; ok {
		return val, true
	}

	return nil, false
}

// Set updates a configuration value.
func (cm *ConfigManager) Set(key string, value interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config[key] = value
	return nil
}

// GetAll returns all configuration values.
func (cm *ConfigManager) GetAll() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range cm.defaults {
		result[k] = v
	}
	for k, v := range cm.config {
		result[k] = v
	}

	return result
}
