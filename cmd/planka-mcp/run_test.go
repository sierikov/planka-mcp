package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunStopsWhenContextEnds checks that cancelling the context ends run even though stdin
// stays open and idle, as it does when a signal arrives while a client is connected.
func TestRunStopsWhenContextEnds(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	t.Cleanup(func() { _ = stdinWriter.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Config{URL: "http://127.0.0.1:1", APIKey: "k", Transport: "stdio", Addr: ":8080"}, stdinReader, io.Discard)
	}()

	time.Sleep(100 * time.Millisecond) // let run block on the idle stdin
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "run did not return after the context was cancelled")
	}
}

// TestRunHTTPServesAndShutsDown checks that the http transport answers /healthz and 401s an
// unauthenticated /mcp call on its listener, and returns context.Canceled once ctx ends.
func TestRunHTTPServesAndShutsDown(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runHTTP(ctx, Config{URL: "http://127.0.0.1:1", Transport: "http"}, ln) }()

	status := func(method, path string) int {
		req, reqErr := http.NewRequestWithContext(ctx, method, base+path, http.NoBody)
		require.NoError(t, reqErr)
		resp, doErr := http.DefaultClient.Do(req)
		require.NoError(t, doErr)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusOK, status(http.MethodGet, "/healthz"))
	assert.Equal(t, http.StatusUnauthorized, status(http.MethodPost, "/mcp"))
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(7 * time.Second):
		require.FailNow(t, "runHTTP did not return after the context was cancelled")
	}
}

// TestRunHTTPReportsBindFailure checks that an address that cannot be bound is an error from
// run, not a log line about serving.
func TestRunHTTPReportsBindFailure(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	err = run(t.Context(), Config{URL: "http://127.0.0.1:1", Transport: "http", Addr: ln.Addr().String()}, nil, io.Discard)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "listen on")
}
