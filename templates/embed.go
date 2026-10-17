// Package templates embeds the seasonal message templates.
package templates

import _ "embed"

//go:embed seasonal.tsv
var Seasonal []byte
