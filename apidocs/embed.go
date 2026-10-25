// Package apidocs embeds the BosBase HTTP API reference so that it can be
// served to AI agents (eg. through the MCP server).
package apidocs

import "embed"

// Reference contains the markdown files of the api-reference directory.
//
//go:embed api-reference/*.md
var Reference embed.FS
