package conn

import (
	"context"
	"io"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/stretchr/testify/assert"
)

// testCtx returns a context for tests (avoids importing context in
// production sources).
func testCtx() context.Context { return context.Background() }

// noopCloser is a no-op io.Closer used by mock tunnel hooks in tests.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// newNoopCloser returns a fresh no-op io.Closer for mock tunnel hooks.
func newNoopCloser() io.Closer { return noopCloser{} }

func TestOpenWithoutSSHNoTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p"}
	_, err := Open(testCtx(), ds)
	assert.Error(t, err) // connection refused, but no SSH path taken
}

func TestSSHDisabledSkipsTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", SSH: &config.SSHConfig{Enable: false}}
	called := false
	_, err := openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig) (string, int, io.Closer, error) {
		called = true
		return "127.0.0.1", 3330, newNoopCloser(), nil
	})
	assert.Error(t, err)
	assert.False(t, called)
}

func TestSSHEnabledUsesTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", SSH: &config.SSHConfig{Enable: true, LocalPort: 3330}}
	called := false
	_, _ = openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig) (string, int, io.Closer, error) {
		called = true
		return "127.0.0.1", 3330, newNoopCloser(), nil
	})
	assert.True(t, called)
}
