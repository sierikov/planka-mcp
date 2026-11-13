package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
)

// Config is the server's runtime configuration.
type Config struct {
	// URL is the base URL of the Planka instance.
	URL string
	// APIKey is the Planka API key; it is used in stdio mode only.
	APIKey string
	// Username and Password log in to Planka instead of an API key (stdio mode
	// only), for users who cannot create API keys: in Planka only admins can.
	Username, Password string
	// APIKeyIgnored is true when both an API key and a login were given: the
	// login wins and the key is dropped.
	APIKeyIgnored bool
	// Transport is "stdio" or "http".
	Transport string
	// Addr is the listen address in http mode.
	Addr string
}

const (
	transportStdio = "stdio"
	transportHTTP  = "http"
	defaultAddr    = ":8080"
)

// loadConfig reads the configuration from the environment, which flags override.
func loadConfig(args []string, getenv func(string) string) (Config, error) {
	getenv = withoutPlaceholders(getenv)
	cfg := Config{
		URL:      getenv("PLANKA_URL"),
		APIKey:   strings.TrimSpace(getenv("PLANKA_API_KEY")),
		Username: strings.TrimSpace(getenv("PLANKA_USERNAME")),
		// A password may start or end with spaces; only the line ending a secret
		// file usually adds is dropped.
		Password:  strings.TrimRight(getenv("PLANKA_PASSWORD"), "\r\n"),
		Transport: getenv("PLANKA_MCP_TRANSPORT"),
		Addr:      getenv("PLANKA_MCP_ADDR"),
	}
	if cfg.Transport == "" {
		cfg.Transport = transportStdio
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}

	fs := newFlags(&cfg)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("invalid flags: %w (see planka-mcp help)", err)
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q (see planka-mcp help)", fs.Arg(0))
	}

	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		return Config{}, errors.New("PLANKA_URL is required (or pass -url)")
	}
	if err := checkURL(cfg.URL); err != nil {
		return Config{}, err
	}
	if err := checkAuth(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// newFlags defines the command-line flags on cfg, whose fields hold the defaults.
func newFlags(cfg *Config) *flag.FlagSet {
	fs := flag.NewFlagSet("planka-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.URL, "url", cfg.URL, "base URL of Planka (env PLANKA_URL)")
	fs.StringVar(&cfg.Transport, "transport", cfg.Transport, "stdio or http (env PLANKA_MCP_TRANSPORT)")
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address in http mode (env PLANKA_MCP_ADDR)")
	return fs
}

// checkURL requires an http or https URL with a host, so that a value such as
// "planka.example.com" fails at startup instead of on every tool call.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("PLANKA_URL %q is not an http(s) URL: use the address you open Planka at, such as https://planka.example.com", raw)
	}
	return nil
}

// checkAuth applies the credential rules of cfg's transport. Stdio needs an API key or a
// username and password; http takes none, because each client sends its own key.
func checkAuth(cfg *Config) error {
	switch cfg.Transport {
	case transportStdio:
		return checkStdioAuth(cfg)
	case transportHTTP:
		if cfg.APIKey != "" {
			return errors.New("PLANKA_API_KEY must not be set in http mode: each client sends its own key")
		}
		if cfg.Username != "" || cfg.Password != "" {
			return errors.New("PLANKA_USERNAME and PLANKA_PASSWORD must not be set in http mode: each client sends its own key")
		}
		return nil
	default:
		return fmt.Errorf("unknown transport %q: use stdio or http", cfg.Transport)
	}
}

// checkStdioAuth requires an API key or a username with a password. When both are given the
// login wins: the key is dropped and APIKeyIgnored is set so main can say so.
func checkStdioAuth(cfg *Config) error {
	login := cfg.Username != "" || cfg.Password != ""
	switch {
	case login && (cfg.Username == "" || cfg.Password == ""):
		return errors.New("PLANKA_USERNAME and PLANKA_PASSWORD go together: set both, or use PLANKA_API_KEY")
	case login && cfg.APIKey != "":
		cfg.APIKey, cfg.APIKeyIgnored = "", true
	case !login && cfg.APIKey == "":
		return errors.New("stdio mode needs PLANKA_API_KEY, or PLANKA_USERNAME and PLANKA_PASSWORD")
	}
	return nil
}

// withoutPlaceholders treats a value that is still an unexpanded MCPB placeholder, such as
// "${user_config.planka_username}", as unset. manifest.json gives every optional field a default,
// but a host that ignores defaults would otherwise pass the placeholder itself, and a bogus
// username would switch an API-key user to a login that cannot work.
func withoutPlaceholders(getenv func(string) string) func(string) string {
	return func(key string) string {
		v := getenv(key)
		if strings.HasPrefix(v, "${user_config.") && strings.HasSuffix(v, "}") {
			return ""
		}
		return v
	}
}

// plainHTTPWarning says which credential would travel to Planka unencrypted, or is "": it is
// set when PLANKA_URL is http:// to a host other than this machine. In stdio mode that is the
// password or the API key; in http mode, the key each client sends. A credential travels with
// the first request, before any redirect to https could help.
func (c Config) plainHTTPWarning() string {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return ""
	}
	what := "your Planka API key"
	switch {
	case c.Transport == transportHTTP:
		what = "each client's Planka API key"
	case c.Username != "":
		what = "your Planka password"
	}
	return "PLANKA_URL is http:// to another machine, so " + what + " travels unencrypted: use the https:// address"
}
