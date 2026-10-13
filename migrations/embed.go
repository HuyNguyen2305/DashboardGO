// Package migrations embeds the SQL migration files so tools and tests don't depend on the working directory.
package migrations

import "embed"

// FS holds every *.up.sql and *.down.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
