package config

import "fmt"

// SettingsError reports that a setting could not be resolved, or a config
// file/value is invalid. Mirrors settings.py's SettingsError.
type SettingsError struct{ msg string }

func (e *SettingsError) Error() string { return e.msg }

func settingsErrorf(format string, args ...any) error {
	return &SettingsError{msg: fmt.Sprintf(format, args...)}
}
