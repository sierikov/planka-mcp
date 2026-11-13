package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 5 * time.Second
	unauthorizedBody  = `send your Planka API key as "Authorization: Bearer <key>"`
	noKey             = "none"
)

// newHTTPHandler serves server over stateless streamable HTTP at /mcp and a liveness probe at
// GET /healthz. Every /mcp request must carry the caller's own Planka key as
// "Authorization: Bearer <key>"; it is rejected with 401 before MCP sees it otherwise, and
// the key reaches the tools through the request context. One line per request goes to logger:
// method, path, status and the first 8 hex characters of the SHA-256 of the key, never the key.
func newHTTPHandler(server *mcp.Server, logger *slog.Logger) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless: true,
			// The SDK's DNS-rebinding guard answers 403 to a request that arrives on a loopback
			// address with another Host, which is every request a host-preserving reverse proxy
			// (Caddy, Traefik) on the same machine forwards. It is safe to turn off: every /mcp
			// request must carry the caller's own Planka key as a bearer key, and the server holds
			// no credentials of its own that a rebound page could borrow.
			DisableLocalhostProtection: true,
		},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// The MCP spec says a server MUST answer 403 to a request whose Origin is invalid. A browser
	// request from another site is refused here; clients that are not browsers send no Origin,
	// and a proxy that keeps the Host header keeps Origin and Host in step.
	mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(requireBearer(mcpHandler)))
	return logRequests(logger, mux)
}

// requireBearer passes the request on with the bearer key stored in its context, or answers 401.
func requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := bearerKey(r.Header.Get("Authorization"))
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, unauthorizedBody, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(planka.WithAPIKey(r.Context(), key)))
	})
}

// bearerKey extracts the key from an "Authorization: Bearer <key>" header value. The scheme is
// matched case-insensitively and must be followed by a space. Extra spaces around the key are
// ignored ("Bearer  k" is k), but a key never contains whitespace, so "Bearer a b" is rejected
// rather than guessed at, as are an empty key, another scheme and an empty header.
func bearerKey(header string) (string, bool) {
	scheme, key, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", false
	}
	return key, true
}

// logRequests logs each finished request, identifying its caller by a short key hash only.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"key", keyFingerprint(r.Header.Get("Authorization")),
		)
	})
}

// keyFingerprint is the first 8 hex characters of the SHA-256 of the bearer key in header, or
// "none" when there is no well-formed key.
func keyFingerprint(header string) string {
	key, ok := bearerKey(header)
	if !ok {
		return noKey
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// statusRecorder remembers the status code a handler wrote. Unwrap lets http.ResponseController
// (which the SDK uses to flush server-sent events) reach the underlying writer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer, which streamed responses need.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// serveHTTP serves handler on ln until ctx ends, then shuts down gracefully and returns ctx's
// error. When the graceful shutdown fails (a slow call outlived the timeout) the remaining
// connections are closed and the failure is logged, but ctx's error is still returned.
//
// There is no ReadTimeout or WriteTimeout: they would cut off streamed (SSE) responses.
func serveHTTP(ctx context.Context, ln net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("serving MCP over HTTP", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed, closing connections", "error", err)
			if closeErr := srv.Close(); closeErr != nil {
				logger.Error("closing the server", "error", closeErr)
			}
		}
		return ctx.Err()
	}
}
