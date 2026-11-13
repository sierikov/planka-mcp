// Command planka-mcp is an MCP server for Planka v2 boards.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // lets TZ work in images without zoneinfo

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/service"
	"github.com/sierikov/planka-mcp/internal/tools"
)

// usage is what `planka-mcp help` and -h print.
const usage = `planka-mcp: an MCP server for Planka v2 kanban boards.

Usage:
  planka-mcp [flags]   serve MCP, over stdio unless -transport http
  planka-mcp version   print the version
  planka-mcp help      print this help

Flags (each overrides its environment variable):
  -url URL             base URL of your Planka (PLANKA_URL), e.g. https://planka.example.com
  -transport MODE      stdio (default) or http (PLANKA_MCP_TRANSPORT)
  -addr ADDRESS        listen address in http mode (PLANKA_MCP_ADDR, default :8080)

Credentials, stdio mode only:
  PLANKA_API_KEY                     your Planka API key
  PLANKA_USERNAME, PLANKA_PASSWORD   log in instead, for users without an API key
In http mode each client sends its own key as "Authorization: Bearer <key>".

Docs: https://github.com/sierikov/planka-mcp
`

func main() {
	if code, ok := command(os.Args[1:], os.Stdout); ok {
		os.Exit(code)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Args[1:], os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprint(os.Stdout, usage)
		return
	}
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	if cfg.APIKeyIgnored {
		logger.Warn("PLANKA_USERNAME and PLANKA_PASSWORD are set, so PLANKA_API_KEY is ignored")
	}
	if msg := cfg.plainHTTPWarning(); msg != "" {
		logger.Warn(msg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, cfg, os.Stdin, os.Stdout)
	stop()
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("planka-mcp stopped", "error", err)
		os.Exit(1)
	}
}

// command runs the commands that are not the server: help and version. It
// reports whether args named one, and the exit code.
func command(args []string, stdout io.Writer) (code int, ok bool) {
	if len(args) != 1 {
		return 0, false
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		_, _ = fmt.Fprint(stdout, usage)
	case "version", "-version", "--version":
		_, _ = fmt.Fprintln(stdout, buildVersion())
	default:
		return 0, false
	}
	return 0, true
}

// newDeps wires the services on a Planka client.
func newDeps(c *planka.Client) tools.Deps {
	catalog := service.NewCatalog(c, c)
	cards := service.NewCards(catalog, c, c, c, c, c, c, c)
	return tools.Deps{
		Catalog:  catalog,
		Cards:    cards,
		Comments: service.NewComments(cards, c),
		Tasks:    service.NewTasks(cards, c),
	}
}

// run serves MCP as cfg says until ctx ends. In stdio mode the protocol runs on stdin and stdout.
func run(ctx context.Context, cfg Config, stdin io.Reader, stdout io.Writer) error {
	switch cfg.Transport {
	case transportStdio:
		client := planka.NewClient(cfg.URL, stdioAuth(cfg))
		defer logout(ctx, client)
		server := tools.NewServer(newDeps(client), buildVersion())
		// The SDK closes the reader when ctx ends, which is what unblocks a read on an idle
		// stdin, so a closable reader must keep its Close. Stdout is never closed.
		reader, ok := stdin.(io.ReadCloser)
		if !ok {
			reader = io.NopCloser(stdin)
		}
		transport := &mcp.IOTransport{
			Reader: reader,
			Writer: nopWriteCloser{stdout},
		}
		return server.Run(ctx, transport)
	case transportHTTP:
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
		}
		return runHTTP(ctx, cfg, ln)
	default:
		return fmt.Errorf("unknown transport %q", cfg.Transport)
	}
}

// stdioAuth is how the stdio client authenticates: the login when one is set, else the API key.
func stdioAuth(cfg Config) planka.Option {
	if cfg.Username != "" {
		return planka.WithLogin(cfg.Username, cfg.Password)
	}
	return planka.WithStaticKey(cfg.APIKey)
}

// logoutTimeout bounds the logout on shutdown, so a Planka that stopped answering cannot hold
// the process open.
const logoutTimeout = 3 * time.Second

// logout ends the client's login session, if it has one, when the server stops. It runs after
// ctx has ended, so it uses its own deadline.
func logout(ctx context.Context, client *planka.Client) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logoutTimeout)
	defer cancel()
	if err := client.Logout(ctx); err != nil {
		slog.Default().Warn("could not log out of Planka", "error", err)
	}
}

// runHTTP serves MCP over HTTP on ln until ctx ends. One client without a static key serves
// every request: each carries its own key in its context.
func runHTTP(ctx context.Context, cfg Config, ln net.Listener) error {
	client := planka.NewClient(cfg.URL)
	server := tools.NewServer(newDeps(client), buildVersion())
	logger := slog.Default() // main installs its stderr logger as the default
	return serveHTTP(ctx, ln, newHTTPHandler(server, logger), logger)
}

// nopWriteCloser leaves the underlying writer open: the process owns stdout.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
