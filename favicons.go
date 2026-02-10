/*
Copyright © 2026 Seednode <seednode@seedno.de>
*/

package main

import (
	"embed"
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"
)

//go:embed favicons/*
var favicons embed.FS

func getFavicon() string {
	return `<link rel="apple-touch-icon" sizes="180x180" href="/favicons/apple-touch-icon.png">
	<link rel="icon" type="image/png" sizes="32x32" href="/favicons/favicon-96x96.png">
	<link rel="manifest" href="/favicons/site.webmanifest" crossorigin="use-credentials">
	<meta name="msapplication-TileColor" content="#da532c">
	<meta name="theme-color" content="#ffffff">`
}

func serveFavicons(cfg *Config, errs chan<- error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		fname := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, cfg.prefix), "/")

		data, err := favicons.ReadFile(fname)
		if err != nil {
			return
		}

		securityHeaders(cfg, w)

		_, err = w.Write(data)
		if err != nil {
			errs <- err

			return
		}
	}
}
