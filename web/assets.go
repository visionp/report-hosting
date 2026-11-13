package web

import "embed"

//go:embed templates/*.html
var Templates embed.FS

//go:embed static/*
var Assets embed.FS
