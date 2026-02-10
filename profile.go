/*
Copyright © 2026 Seednode <seednode@seedno.de>
*/

package main

import (
	"net/http/pprof"

	"github.com/julienschmidt/httprouter"
)

func registerProfileHandlers(cfg *Config, mux *httprouter.Router) {
	mux.Handler("GET", cfg.prefix+"/pprof/allocs", pprof.Handler("allocs"))
	mux.Handler("GET", cfg.prefix+"/pprof/block", pprof.Handler("block"))
	mux.Handler("GET", cfg.prefix+"/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handler("GET", cfg.prefix+"/pprof/heap", pprof.Handler("heap"))
	mux.Handler("GET", cfg.prefix+"/pprof/mutex", pprof.Handler("mutex"))
	mux.Handler("GET", cfg.prefix+"/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.HandlerFunc("GET", cfg.prefix+"/pprof/cmdline", pprof.Cmdline)
	mux.HandlerFunc("GET", cfg.prefix+"/pprof/profile", pprof.Profile)
	mux.HandlerFunc("GET", cfg.prefix+"/pprof/symbol", pprof.Symbol)
	mux.HandlerFunc("GET", cfg.prefix+"/pprof/trace", pprof.Trace)
}
