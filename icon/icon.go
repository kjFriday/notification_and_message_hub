// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: icon/icon.go
// About: Package icon embeds the Notify hub's pixel icon (Silk "bell", CC BY 2.5; see README.md).
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package icon embeds the Notify hub's pixel icon (Silk "bell", CC BY 2.5; see README.md).
package icon

import _ "embed"

var (
	//go:embed app.ico
	ICO []byte
	//go:embed app-32.png
	PNG32 []byte
	//go:embed app-256.png
	PNG256 []byte
)

// ==== stamp v1 | author=kjFriday | hmac-sha256=75764d53a4c63905403707bc424c30d815748b3c380d99085118de5aa077a41b ====
