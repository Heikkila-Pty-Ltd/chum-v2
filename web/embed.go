// Package web provides the embedded filesystem for the CHUM dashboard UI.
package web

import "embed"

//go:embed *.html *.css *.js views/*.js
var Assets embed.FS
