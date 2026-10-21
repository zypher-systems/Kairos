// Package scripts embeds installer resources into the server binary.
package scripts

import _ "embed"

// AgentInstallScript is the agent installer served at
// /api/v1/agent/install.sh and used by push-install.
//
//go:embed agent-install.sh
var AgentInstallScript string
