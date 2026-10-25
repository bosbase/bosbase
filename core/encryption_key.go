package core

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

// AllowSettingsResetEnv allows overwriting stored settings that can't be decrypted
// (eg. when the original encryption key was lost).
const AllowSettingsResetEnv = "BOSBASE_ALLOW_SETTINGS_RESET"

var encryptionKeyWarned sync.Map

// IsValidEncryptionKey reports whether key can be used as AES key (16, 24 or 32 bytes).
func IsValidEncryptionKey(key string) bool {
	switch len(key) {
	case 16, 24, 32:
		return true
	default:
		return false
	}
}

// EncryptionKey returns the app encryption key (the value of the app.EncryptionEnv() env variable).
//
// It returns an empty string when the key is not configured or has an invalid format,
// in which case the data is stored unencrypted instead of failing (a warning is logged once).
func EncryptionKey(app App) string {
	envName := app.EncryptionEnv()
	if envName == "" {
		return ""
	}

	key := os.Getenv(envName)
	if key == "" || IsValidEncryptionKey(key) {
		return key
	}

	if _, warned := encryptionKeyWarned.LoadOrStore(envName, true); !warned {
		logger := slog.Default()
		if l := app.Logger(); l != nil {
			logger = l
		}
		logger.Warn(
			"Invalid encryption key: it must be exactly 32 characters (AES-256; 16 or 24 are also accepted). "+
				"Encryption is disabled and secrets are stored unencrypted.",
			"env", envName,
			"length", len(key),
			"hasWhitespace", strings.ContainsAny(key, " \t\r\n"),
		)
	}

	return ""
}

// StoreKeySettingsUnreadable is set in the app store when the stored settings
// couldn't be decrypted during the last reload.
const StoreKeySettingsUnreadable = "bosbaseSettingsUnreadable"

// IsSettingsUnreadable reports whether the stored settings couldn't be decrypted
// during the last reload (the app is running with the default settings).
func IsSettingsUnreadable(app App) bool {
	v, _ := app.Store().Get(StoreKeySettingsUnreadable).(bool)
	return v
}

func allowSettingsReset() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(AllowSettingsResetEnv)))
	return v == "1" || v == "true" || v == "yes"
}
