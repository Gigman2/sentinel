// Package migrations embeds Sentinel's SQL schema migrations (goose format).
// The Python worker's tests apply the same files, so each Up section must run
// as a single string: do not use goose StatementBegin/StatementEnd blocks.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
