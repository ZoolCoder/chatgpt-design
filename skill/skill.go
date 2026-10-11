// Package skill carries the agent skill, so the binary can install it.
package skill

import _ "embed"

//go:embed SKILL.md
var Markdown []byte
