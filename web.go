package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
)

const (
	logDate string        = `2006-01-02T15:04:05.000-07:00`
	timeout time.Duration = 10 * time.Second
)

func securityHeaders(cfg *Config, w http.ResponseWriter) {
	w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-site")
	w.Header().Set("Permissions-Policy", "geolocation=(), midi=(), sync-xhr=(), microphone=(), camera=(), magnetometer=(), gyroscope=(), fullscreen=(), payment=()")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'")

	if cfg.scheme() == "https" {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
	}
}

func realIP(r *http.Request) string {
	host, port, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		if net.ParseIP(ip) != nil {
			host = ip
		}
	} else if ip := r.Header.Get("X-Real-IP"); ip != "" {
		if net.ParseIP(ip) != nil {
			host = ip
		}
	}
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return host + ":" + port
	}
	return host
}

func serveVersion(cfg *Config, errs chan<- error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		startTime := time.Now()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		securityHeaders(cfg, w)
		w.WriteHeader(http.StatusOK)

		written, err := w.Write([]byte("partybox v" + releaseVersion + "\n"))
		if err != nil {
			errs <- err

			return
		}

		logf(cfg, "SERVE: Version page (%s) to %s in %s",
			humanReadableSize(int64(written)),
			realIP(r),
			time.Since(startTime).Round(time.Microsecond),
		)
	}
}

func ServePage(ctx context.Context, cfg *Config, args []string) error {
	var err error

	timeZone := os.Getenv("TZ")
	if timeZone != "" {
		time.Local, err = time.LoadLocation(timeZone)
		if err != nil {
			return err
		}
	}

	logf(cfg, "START: partybox v%s", releaseVersion)

	mux := httprouter.New()

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.bind, strconv.Itoa(cfg.port)),
		Handler:           mux,
		IdleTimeout:       10 * time.Minute,
		ReadTimeout:       timeout,
		ReadHeaderTimeout: timeout,
		WriteTimeout:      timeout,
	}

	mux.PanicHandler = func(w http.ResponseWriter, r *http.Request, i any) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		securityHeaders(cfg, w)
		w.WriteHeader(http.StatusInternalServerError)

		io.WriteString(w, newPage("Server Error", "An error has occurred. Please try again."))
	}

	errs := make(chan error, 64)

	cfg.prefix = strings.TrimSuffix(cfg.prefix, "/")

	mux.GET(cfg.prefix+"/", serveHomePage(cfg))

	mux.GET(cfg.prefix+"/favicons/*favicon", serveFavicons(cfg, errs))

	mux.GET(cfg.prefix+"/favicon.webp", serveFavicons(cfg, errs))

	mux.GET(cfg.prefix+"/healthz", serveHealthCheck(cfg, errs))

	mux.GET(cfg.prefix+"/robots.txt", serveRobots(cfg, errs))

	mux.GET(cfg.prefix+"/version", serveVersion(cfg, errs))

	if cfg.profile {
		registerProfileHandlers(cfg, mux)
	}

	registerCelebrityGame(cfg, "/celebrity", mux)

	go func() {
		var err error
		if cfg.tlsKey != "" && cfg.tlsCert != "" {
			logf(cfg, "SERVE: Listening on %s://%s%s/", cfg.scheme(), srv.Addr, cfg.prefix)
			err = srv.ListenAndServeTLS(cfg.tlsCert, cfg.tlsKey)
		} else {
			logf(cfg, "SERVE: Listening on %s://%s%s/", cfg.scheme(), srv.Addr, cfg.prefix)
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("%s | ERROR: %v\n", time.Now().Format(logDate), err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	return nil
}
