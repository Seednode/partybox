/*
Copyright © 2025 Seednode <seednode@seedno.de>
*/

package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

func logf(cfg *Config, format string, args ...any) {
	if !cfg.verbose {
		return
	}

	log.Printf("%s | "+format, append([]any{time.Now().Format(logDate)}, args...)...)
}

func newPage(title, body string) string {
	var htmlBody strings.Builder

	htmlBody.WriteString(`<!DOCTYPE html><html lang="en"><head>`)
	htmlBody.WriteString(getFavicon())
	htmlBody.WriteString(`<style>`)
	htmlBody.WriteString(`html,body,a{display:block;height:100%;width:100%;text-decoration:none;color:inherit;cursor:auto;}</style>`)
	htmlBody.WriteString(fmt.Sprintf("<title>%s</title></head>", title))
	htmlBody.WriteString(fmt.Sprintf("<body><a href=\"/\">%s</a></body></html>", body))

	return htmlBody.String()
}
