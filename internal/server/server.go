// Package server is harbrr's composition root: it mounts the *arr-facing Torznab
// handler and the management API on one HTTP listener but separate route trees
// (architecture invariant #3), serves the embedded OpenAPI spec + Swagger UI,
// supports a base path, and shuts down gracefully.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// Deps are the handlers and spec the server mounts.
type Deps struct {
	// Management is the OpenAPI management router (serves /healthz + /api/...).
	Management http.Handler
	// Torznab is the *arr-facing handler (serves /api/v2.0/indexers/...).
	Torznab http.Handler
	// Spec is the embedded OpenAPI document, served at /api/openapi.yaml.
	Spec []byte
	// DocsUI is the embedded Swagger UI page, served at /api/docs.
	DocsUI []byte
	Logger zerolog.Logger
}

// Config is the listener + base-path configuration.
type Config struct {
	Addr     string
	BasePath string
	// ShutdownTimeout bounds graceful shutdown; defaults to 15s when zero.
	ShutdownTimeout time.Duration
}

// Server wraps the HTTP server with graceful lifecycle.
type Server struct {
	http     *http.Server
	log      zerolog.Logger
	shutdown time.Duration
}

// New assembles the root router and HTTP server. The Torznab tree
// (/api/v2.0/indexers/*) is mounted ahead of the management catch-all so the two
// contracts stay on separate trees; the management router (which owns /healthz and
// /api/*) handles everything else. When BasePath is set, it is stripped before
// routing so internal patterns stay absolute.
func New(deps Deps, cfg Config) *Server {
	root := chi.NewRouter()
	root.Use(chimw.RequestID, chimw.Recoverer, requestLogger(deps.Logger))

	root.Handle("/api/v2.0/indexers/*", deps.Torznab)
	root.Get("/api/openapi.yaml", specHandler(deps.Spec))
	root.Get("/api/docs", docsHandler(deps.DocsUI))
	root.Handle("/*", deps.Management)

	var h http.Handler = root
	if cfg.BasePath != "" {
		h = http.StripPrefix(cfg.BasePath, root)
	}

	timeout := cfg.ShutdownTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Server{
		http: &http.Server{
			Addr:              cfg.Addr,
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		log:      deps.Logger,
		shutdown: timeout,
	}
}

// Handler exposes the root handler (for httptest-based end-to-end tests).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("server: listen: %w", err)
	case <-ctx.Done():
		s.log.Info().Msg("server: shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), s.shutdown)
		defer cancel()
		if err := s.http.Shutdown(shutCtx); err != nil {
			return fmt.Errorf("server: shutdown: %w", err)
		}
		return nil
	}
}

// specHandler serves the embedded OpenAPI document.
func specHandler(spec []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(spec)
	}
}

// docsHandler serves the embedded Swagger UI page (the interactive API docs). It is
// public, like the spec it renders, and read-only.
func docsHandler(page []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}
}

// requestLogger logs each request with the URL redacted (a Torznab URL carries an
// apikey/passkey), so request logs never leak a secret.
func requestLogger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Debug().
				Str("method", r.Method).
				Str("path", apphttp.RedactURL(r.URL.RequestURI())).
				Int("status", ww.Status()).
				Dur("took", time.Since(start)).
				Msg("http request")
		})
	}
}
