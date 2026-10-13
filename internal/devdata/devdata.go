// Package devdata holds seed data for local development and integration tests.
package devdata

import _ "embed"

// ScreenshotSQL reproduces the dashboard screenshots for October 2026 (GMT+7).
// It expects an empty database whose ID counters start at 1.
//
//go:embed screenshot.sql
var ScreenshotSQL string
