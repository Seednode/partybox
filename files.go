/*
Copyright © 2026 Seednode <seednode@seedno.de>
*/

package main

import (
	"fmt"
)

func humanReadableSize(bytes int64) string {
	const unit int64 = 1000
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(bytes)/float64(div),
		"kMGTPE"[exp])
}
