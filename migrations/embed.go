// Package migrations embeds versioned SQL so runtime does not depend on the
// working directory or an editable external schema file.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
