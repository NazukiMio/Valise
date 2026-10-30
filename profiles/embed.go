// Package profiles embeds the official Harness Profiles (*.yaml) shipped
// with the client. It contains no harness knowledge itself.
package profiles

import "embed"

// FS holds every official profile.
//
//go:embed *.yaml
var FS embed.FS
