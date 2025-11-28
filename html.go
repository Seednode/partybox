/*
Copyright © 2025 Seednode <seednode@seedno.de>
*/

package main

import (
	"embed"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
)

//go:embed assets/*
var assets embed.FS

func serveHomePage(cfg *Config) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		securityHeaders(cfg, w)
		cspHome(cfg, w)
	}
}

func serveHealthCheck(cfg *Config, errs chan<- error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		securityHeaders(cfg, w)

		_, err := w.Write([]byte("Ok\n"))
		if err != nil {
			errs <- err

			return
		}
	}
}

func serveAssets(cfg *Config, errs chan<- error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		fname := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, cfg.prefix), "/")

		data, err := assets.ReadFile(fname)
		if err != nil {
			return
		}

		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Expires", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		securityHeaders(cfg, w)

		ext := strings.ToLower(filepath.Ext(fname))
		switch ext {
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset-utf-8")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset-utf-8")
		case ".wasm":
			w.Header().Set("Content-Type", "application/wasm")
		case ".woff2":
			w.Header().Set("Content-Type", "font/woff2")
		}

		_, err = w.Write(data)
		if err != nil {
			errs <- err

			return
		}
	}
}

func serveRobots(cfg *Config, errs chan<- error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		data := `User-agent: Amazonbot
Disallow: /

User-agent: Applebot-Extended
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: meta-externalagent
Disallow: /`

		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Expires", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		securityHeaders(cfg, w)

		_, err := w.Write([]byte(data))
		if err != nil {
			errs <- err

			return
		}
	}
}

func registerHome(cfg *Config, path string, mux *httprouter.Router) {
	mux.GET(path, serveHomePage(cfg))
}
