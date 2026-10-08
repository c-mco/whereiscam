package web

import (
	"embed"
)

// FS contains embedded template and static assets.
//
//go:embed templates/* static/*
var FS embed.FS
