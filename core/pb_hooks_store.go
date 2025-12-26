package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// syncPbHooksFilesystem rebuilds the pb_hooks directory from the records stored in the pb_hooks table.
func (app *BaseApp) syncPbHooksFilesystem() error {
	hooksDir := filepath.Clean(filepath.Join(app.DataDir(), "../pb_hooks"))

	if hooksDir == "." || hooksDir == string(filepath.Separator) || hooksDir == "" {
		return fmt.Errorf("invalid pb_hooks directory: %s", hooksDir)
	}

	absHooksDir, err := filepath.Abs(hooksDir)
	if err != nil {
		return err
	}

	// always recreate the hooks directory so we start from a clean slate each time
	if err := os.RemoveAll(absHooksDir); err != nil {
		return err
	}

	if err := os.MkdirAll(absHooksDir, os.ModePerm); err != nil {
		return err
	}

	type storedHook struct {
		Filename string `db:"filename"`
		Content  string `db:"content"`
	}

	var stored []storedHook

	if err := app.DB().Select("filename", "content").From("pb_hooks").OrderBy("filename ASC").All(&stored); err != nil {
		return err
	}

	for _, hook := range stored {
		name := strings.TrimSpace(hook.Filename)
		if name == "" {
			continue
		}

		cleaned := filepath.Base(filepath.Clean(name))
		if cleaned == "." || cleaned == string(filepath.Separator) {
			continue
		}

		target := filepath.Join(absHooksDir, cleaned)

		rel, err := filepath.Rel(absHooksDir, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}

		if err := os.WriteFile(target, []byte(hook.Content), 0644); err != nil {
			return err
		}
	}

	return nil
}
