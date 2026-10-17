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
