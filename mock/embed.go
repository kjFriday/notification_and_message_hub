// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: mock/embed.go
// About: Package mock embeds the snapshot (events.json, customers.json) so the programs run...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package mock embeds the snapshot (events.json, customers.json) so the programs run without a database.
package mock

import "embed"

//go:embed events.json customers.json
var FS embed.FS

// ==== stamp v1 | author=kjFriday | hmac-sha256=6149d07435938f691f70a44b6990402580cc26b6f52375a093b9b95a107c2fe0 ====
