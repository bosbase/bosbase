package cmd

import (
	"errors"
	"fmt"

	"github.com/fatih/color"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/spf13/cobra"
)

// NewSettingsCommand creates and returns new command for managing
// app settings (update backend service URL).
func NewSettingsCommand(app core.App) *cobra.Command {
	command := &cobra.Command{
		Use:   "settings",
		Short: "Manage app settings",
	}

	command.AddCommand(settingsUpdateURLCommand(app))

	return command
}

func settingsUpdateURLCommand(app core.App) *cobra.Command {
	command := &cobra.Command{
		Use:          "update-url",
		Example:      "settings update-url http://localhost:8090",
		Short:        "Updates the Backend service URL (AppURL) in settings",
		SilenceUsage: true,
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("missing URL argument")
			}

			url := args[0]
			if url == "" {
				return errors.New("URL cannot be empty")
			}

			// Validate URL format
			if err := is.URL.Validate(url); err != nil {
				return fmt.Errorf("invalid URL format: %w", err)
			}

			// Clone current settings to avoid modifying the original directly
			settings, err := app.Settings().Clone()
			if err != nil {
				return fmt.Errorf("failed to clone settings: %w", err)
			}

			// Update AppURL
			settings.Meta.AppURL = url

			// Save settings
			if err := app.Save(settings); err != nil {
				return fmt.Errorf("failed to update Backend service URL: %w", err)
			}

			color.Green("Successfully updated Backend service URL to %q!", url)
			return nil
		},
	}

	return command
}

