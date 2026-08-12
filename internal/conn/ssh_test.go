package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"os"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
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
	_, err := openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig, int) (string, int, io.Closer, error) {
		called = true
		return "127.0.0.1", 3330, newNoopCloser(), nil
	})
	assert.Error(t, err)
	assert.False(t, called)
}

func TestSSHEnabledUsesTunnel(t *testing.T) {
	ds := config.Datasource{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", SSH: &config.SSHConfig{Enable: true, LocalPort: 3330}}
	called := false
	_, _ = openWithTunnelHook(testCtx(), ds, func(*config.SSHConfig, int) (string, int, io.Closer, error) {
		called = true
		return "127.0.0.1", 3330, newNoopCloser(), nil
	})
	assert.True(t, called)
}

// TestSSHPassesConnectTimeout 验证 openWithTunnelHook 把 ds.ConnectTimeout 透传给
// tunnel hook（让 SSH 拨号超时与 MySQL 一致，修复 #27 硬编码 10s）。
func TestSSHPassesConnectTimeout(t *testing.T) {
	ds := config.Datasource{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		ConnectTimeout: 7,
		SSH:            &config.SSHConfig{Enable: true, LocalPort: 3330},
	}
	var gotTimeout int
	_, _ = openWithTunnelHook(testCtx(), ds, func(_ *config.SSHConfig, ct int) (string, int, io.Closer, error) {
		gotTimeout = ct
		return "127.0.0.1", 3330, newNoopCloser(), nil
	})
	assert.Equal(t, 7, gotTimeout)
}

// TestBuildHostKeyCallbackInsecure 验证 InsecureIgnoreHostKey=true 时返回
// InsecureIgnoreHostKey 回调（不依赖 known_hosts 文件）。
func TestBuildHostKeyCallbackInsecure(t *testing.T) {
	cfg := &config.SSHConfig{InsecureIgnoreHostKey: true}
	cb, err := buildHostKeyCallback(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, cb)
}

// TestBuildHostKeyCallbackDefaultRejects 验证默认配置（InsecureIgnoreHostKey=false,
// KnownHostsFile 指向不存在的文件）应返回明确 error。
func TestBuildHostKeyCallbackDefaultRejects(t *testing.T) {
	cfg := &config.SSHConfig{
		KnownHostsFile:        "/this/path/does/not/exist/known_hosts",
		InsecureIgnoreHostKey: false,
	}
	_, err := buildHostKeyCallback(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "known_hosts file")
	assert.Contains(t, err.Error(), "not found")
}

// TestBuildHostKeyCallbackNilCfg 兜底：cfg 为 nil 时也应安全返回 error（不 panic）。
func TestBuildHostKeyCallbackNilCfg(t *testing.T) {
	_, err := buildHostKeyCallback(nil)
	assert.Error(t, err)
}

// TestEstablishTunnelDefaultRejectsMissingKnownHosts 验证 establishTunnel 在默认
// host key 校验且 known_hosts 不存在时返回 error。
func TestEstablishTunnelDefaultRejectsMissingKnownHosts(t *testing.T) {
	keyPath := writeTestKey(t)
	cfg := &config.SSHConfig{
		Enable:                true,
		Host:                  "127.0.0.1",
		Port:                  1, // 不实际连上
		User:                  "u",
		KeyPath:               keyPath,
		KnownHostsFile:        "/this/path/does/not/exist/known_hosts",
		InsecureIgnoreHostKey: false,
	}
	_, _, _, err := establishTunnel(cfg, 0)
	assert.Error(t, err)
	// host key 校验在 ssh.Dial 之前，应优先报 known_hosts 错误。
	assert.Contains(t, err.Error(), "known_hosts file")
}

// TestEstablishTunnelInsecureSkipsKnownHosts 验证 InsecureIgnoreHostKey=true 时
// 跳过 known_hosts 检查，进入 ssh.Dial 阶段（因连不上真实 SSH 返回 dial 错误）。
func TestEstablishTunnelInsecureSkipsKnownHosts(t *testing.T) {
	keyPath := writeTestKey(t)
	cfg := &config.SSHConfig{
		Enable:                true,
		Host:                  "127.0.0.1",
		Port:                  1,
		User:                  "u",
		KeyPath:               keyPath,
		InsecureIgnoreHostKey: true,
	}
	_, _, _, err := establishTunnel(cfg, 0)
	assert.Error(t, err)
	// 不应包含 known_hosts 错误，应进入 ssh dial 阶段。
	assert.NotContains(t, err.Error(), "known_hosts file")
	assert.Contains(t, err.Error(), "ssh dial")
}

// TestEstablishTunnelRejectsOpenPrivateKeyMode 验证私钥文件权限过宽（群组/其他可读，
// mode 0644）时 establishTunnel 在读私钥前就拒绝，返回明确的权限 error。
// 修复 #15：原实现直接 os.ReadFile，未校验 mode。
func TestEstablishTunnelRejectsOpenPrivateKeyMode(t *testing.T) {
	keyPath := writeTestKey(t)
	// writeTestKey 默认写 0600；这里故意放宽到 0644 触发权限拒绝。
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg := &config.SSHConfig{
		Enable:                true,
		Host:                  "127.0.0.1",
		Port:                  1,
		User:                  "u",
		KeyPath:               keyPath,
		InsecureIgnoreHostKey: true, // 跳过 known_hosts，确保只有权限这一道闸
	}
	_, _, _, err := establishTunnel(cfg, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too open")
	assert.Contains(t, err.Error(), "0600")
	// 不应进入 ssh dial 阶段（权限校验在读私钥前）。
	assert.NotContains(t, err.Error(), "ssh dial")
}

// TestEstablishTunnelAcceptsStrictPrivateKeyMode 验证 0600 权限的私钥不报权限 error
// （可能因 key 格式或 ssh dial 失败报其他 error，但不应是权限 error）。
func TestEstablishTunnelAcceptsStrictPrivateKeyMode(t *testing.T) {
	keyPath := writeTestKey(t) // 默认 0600
	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg := &config.SSHConfig{
		Enable:                true,
		Host:                  "127.0.0.1",
		Port:                  1,
		User:                  "u",
		KeyPath:               keyPath,
		InsecureIgnoreHostKey: true,
	}
	_, _, _, err := establishTunnel(cfg, 0)
	// 这里会因连不上 SSH 报 ssh dial error，但绝不应是权限 error。
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "too open")
}

// TestEstablishTunnelMissingKeyFileErrors 验证 KeyPath 不存在时返回明确 error
// （os.Stat 失败），而非 panic。
func TestEstablishTunnelMissingKeyFileErrors(t *testing.T) {
	cfg := &config.SSHConfig{
		Enable:                true,
		Host:                  "127.0.0.1",
		Port:                  1,
		User:                  "u",
		KeyPath:               "/this/path/does/not/exist/id_ed25519",
		InsecureIgnoreHostKey: true,
	}
	_, _, _, err := establishTunnel(cfg, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ssh key")
}

// writeTestKey 写一个临时 ed25519 私钥供测试使用（establishTunnel 需要可解析的 key）。
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	// 用 ssh.MarshalPrivateKey（go1.22+ golang.org/x/crypto/ssh）生成 OpenSSH 私钥。
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	dir := t.TempDir()
	p := dir + "/id_ed25519"
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return p
}
