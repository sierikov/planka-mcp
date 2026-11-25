//go:build unix

package service

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamedPipeIsRefusedWithoutBlocking(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0o600))

	_, err := readUpload(pipe)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}
