// Package agentkit embeds the agent-kit skills shipped with the client
// (docs/AGENT_KIT.md section 4).
package agentkit

import "embed"

// Skills holds skills/<name>/SKILL.md.
//
//go:embed skills
var Skills embed.FS
