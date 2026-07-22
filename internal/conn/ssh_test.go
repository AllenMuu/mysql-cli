package conn

import (
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestOpenWithoutSSHNoTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p"}
	_, err := Open(testCtx(), ds)
	assert.Error(t, err) // connection refused, but no SSH path taken
}

func TestSSHDisabledSkipsTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", SSH: &config.SSHConfig{Enable: false}}
	called := false
	_, err := openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig) (string, int, error) {
		called = true
		return "127.0.0.1", 3330, nil
	})
	assert.Error(t, err)
	assert.False(t, called)
}

func TestSSHEnabledUsesTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", SSH: &config.SSHConfig{Enable: true, LocalPort: 3330}}
	called := false
	_, _ = openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig) (string, int, error) {
		called = true
		return "127.0.0.1", 3330, nil
	})
	assert.True(t, called)
}
