// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: templates/embed.go
// About: Package templates embeds the seasonal message templates.
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package templates embeds the seasonal message templates.
package templates

import _ "embed"

//go:embed seasonal.tsv
var Seasonal []byte

// ==== stamp v1 | author=kjFriday | hmac-sha256=21e3e915692dc1a596c6f3839b0737dd205cce552cd5caf74b398ad15a4b227e ====
