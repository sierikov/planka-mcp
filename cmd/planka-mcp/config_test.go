package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name:    "missing url",
			env:     map[string]string{"PLANKA_API_KEY": "k"},
			wantErr: "PLANKA_URL",
		},
		{
			name:    "stdio without key",
			env:     map[string]string{"PLANKA_URL": "http://p"},
			wantErr: "stdio mode needs PLANKA_API_KEY, or PLANKA_USERNAME and PLANKA_PASSWORD",
		},
		{
			name: "stdio with username and password",
			env:  map[string]string{"PLANKA_URL": "http://p", "PLANKA_USERNAME": " ann ", "PLANKA_PASSWORD": " s3cret pass \n"},
			want: Config{URL: "http://p", Username: "ann", Password: " s3cret pass ", Transport: "stdio", Addr: ":8080"},
		},
		{
			name: "login wins over api key",
			env:  map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k", "PLANKA_USERNAME": "ann", "PLANKA_PASSWORD": "pw"},
			want: Config{URL: "http://p", Username: "ann", Password: "pw", Transport: "stdio", Addr: ":8080", APIKeyIgnored: true},
		},
		{
			name: "unexpanded MCPB placeholders count as unset",
			env: map[string]string{ //nolint:gosec // G101: MCPB placeholder strings, not credentials
				"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k",
				"PLANKA_USERNAME": "${user_config.planka_username}", "PLANKA_PASSWORD": "${user_config.planka_password}",
			},
			want: Config{URL: "http://p", APIKey: "k", Transport: "stdio", Addr: ":8080"},
		},
		{
			name:    "username without password",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_USERNAME": "ann"},
			wantErr: "PLANKA_USERNAME and PLANKA_PASSWORD go together",
		},
		{
			name:    "password without username",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k", "PLANKA_PASSWORD": "pw"},
			wantErr: "PLANKA_USERNAME and PLANKA_PASSWORD go together",
		},
		{
			name:    "http with login",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_MCP_TRANSPORT": "http", "PLANKA_USERNAME": "ann", "PLANKA_PASSWORD": "pw"},
			wantErr: "PLANKA_USERNAME and PLANKA_PASSWORD must not be set in http mode: each client sends its own key",
		},
		{
			name:    "http with key",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k", "PLANKA_MCP_TRANSPORT": "http"},
			wantErr: "PLANKA_API_KEY must not be set in http mode: each client sends its own key",
		},
		{
			name:    "unknown transport",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_MCP_TRANSPORT": "carrier-pigeon"},
			wantErr: `unknown transport "carrier-pigeon"`,
		},
		{
			name: "stdio defaults",
			env:  map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k"},
			want: Config{URL: "http://p", APIKey: "k", Transport: "stdio", Addr: ":8080"},
		},
		{
			name: "api key is trimmed",
			env:  map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k\n"},
			want: Config{URL: "http://p", APIKey: "k", Transport: "stdio", Addr: ":8080"},
		},
		{
			name:    "blank api key counts as missing",
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": " \n"},
			wantErr: "stdio mode needs PLANKA_API_KEY, or PLANKA_USERNAME and PLANKA_PASSWORD",
		},
		{
			name: "http from env",
			env:  map[string]string{"PLANKA_URL": "http://p", "PLANKA_MCP_TRANSPORT": "http", "PLANKA_MCP_ADDR": ":9000"},
			want: Config{URL: "http://p", Transport: "http", Addr: ":9000"},
		},
		{
			name: "flags override env",
			args: []string{"-transport", "http", "-addr", ":1234", "-url", "http://flag"},
			env:  map[string]string{"PLANKA_URL": "http://env", "PLANKA_MCP_TRANSPORT": "stdio", "PLANKA_MCP_ADDR": ":9000"},
			want: Config{URL: "http://flag", Transport: "http", Addr: ":1234"},
		},
		{
			name:    "bad flag",
			args:    []string{"-nope"},
			env:     map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k"},
			wantErr: "invalid flags",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(tt.args, env(tt.env))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPlainHTTPWarning(t *testing.T) {
	tests := map[string]struct {
		cfg  Config
		want string // the credential named, "" for no warning
	}{
		"https":                  {Config{URL: "https://planka.example.com", Username: "ann"}, ""},
		"http to another host":   {Config{URL: "http://planka.example.com", Username: "ann"}, "your Planka password"},
		"http on a LAN address":  {Config{URL: "http://192.168.1.20:3000", Username: "ann"}, "your Planka password"},
		"http to localhost":      {Config{URL: "http://localhost:3333", Username: "ann"}, ""},
		"http to 127.0.0.1":      {Config{URL: "http://127.0.0.1:3333", Username: "ann"}, ""},
		"http to ::1":            {Config{URL: "http://[::1]:3333", Username: "ann"}, ""},
		"stdio with an API key":  {Config{URL: "http://planka.example.com", APIKey: "k", Transport: transportStdio}, "your Planka API key"},
		"http mode":              {Config{URL: "http://planka.example.com", Transport: transportHTTP}, "each client's Planka API key"},
		"http mode to localhost": {Config{URL: "http://localhost:3333", Transport: transportHTTP}, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := tc.cfg.plainHTTPWarning()
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, "PLANKA_URL is http:// to another machine, so "+tc.want+" travels unencrypted: use the https:// address", got)
		})
	}
}

func TestLoadConfigChecksTheURL(t *testing.T) {
	for _, bad := range []string{"planka.example.com", "ftp://planka.example.com", "https://", "http:///boards"} {
		t.Run(bad, func(t *testing.T) {
			_, err := loadConfig(nil, env(map[string]string{"PLANKA_URL": bad, "PLANKA_API_KEY": "k"}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not an http(s) URL: use the address you open Planka at")
		})
	}
}

func TestLoadConfigHelpAndExtraArguments(t *testing.T) {
	ok := env(map[string]string{"PLANKA_URL": "http://p", "PLANKA_API_KEY": "k"})

	_, err := loadConfig([]string{"-url", "http://p", "-h"}, ok)
	require.ErrorIs(t, err, flag.ErrHelp, "main prints the usage for it")

	_, err = loadConfig([]string{"serve"}, ok)
	require.EqualError(t, err, `unexpected argument "serve" (see planka-mcp help)`)
}

func TestCommand(t *testing.T) {
	for _, arg := range []string{"help", "-h", "-help", "--help"} {
		var out strings.Builder
		code, ok := command([]string{arg}, &out)
		assert.True(t, ok, arg)
		assert.Zero(t, code, arg)
		assert.Equal(t, usage, out.String(), arg)
	}
	for _, arg := range []string{"version", "-version", "--version"} {
		var out strings.Builder
		code, ok := command([]string{arg}, &out)
		assert.True(t, ok, arg)
		assert.Zero(t, code, arg)
		assert.Equal(t, buildVersion()+"\n", out.String(), arg)
	}
	for _, args := range [][]string{nil, {"-url", "http://p"}, {"serve"}} {
		_, ok := command(args, io.Discard)
		assert.False(t, ok, "%q is for the server", args)
	}
}

// TestUsageNamesEveryFlag keeps the hand-written usage in step with the flags.
func TestUsageNamesEveryFlag(t *testing.T) {
	newFlags(&Config{}).VisitAll(func(f *flag.Flag) {
		assert.Contains(t, usage, "  -"+f.Name+" ", f.Name)
	})
}
