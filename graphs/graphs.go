// Package graphs holds the graph files. They are built into the factory
// binary, so the MCP server can start a run from any folder.
package graphs

import "embed"

//go:embed *.yaml
var FS embed.FS
