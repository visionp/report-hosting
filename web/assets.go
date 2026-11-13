package web

import "embed"

//go:embed templates/dashboard.html
var Dashboard string

//go:embed static/*
var Assets embed.FS
