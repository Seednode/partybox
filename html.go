/*
Copyright © 2025 Seednode <seednode@seedno.de>
*/

package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
)

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
