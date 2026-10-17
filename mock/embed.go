// Package mock embeds the snapshot (events.json, customers.json) so the programs run without a database.
package mock

import "embed"

//go:embed events.json customers.json
var FS embed.FS
