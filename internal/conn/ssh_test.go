package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

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

// --- A5/A6：in-process SSH 服务器（支持 direct-tcpip 转发）---

// startTestSSHServer 启动一个最小 SSH 服务器（NoClientAuth + direct-tcpip
// 端口转发），返回监听地址。供 establishTunnel 端到端测试使用。
func startTestSSHServer(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	assert.NoError(t, err)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleTestSSHConn(c, cfg)
		}
	}()
	return ln.Addr().String()
}

// handleTestSSHConn 只实现 direct-tcpip 通道：把目标地址拨通后双向管道。
func handleTestSSHConn(c net.Conn, cfg *ssh.ServerConfig) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "direct-tcpip" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		var payload struct {
			Raddr string
			Rport uint32
			Laddr string
			Lport uint32
		}
		if err := ssh.Unmarshal(newChan.ExtraData(), &payload); err != nil {
			newChan.Reject(ssh.Prohibited, "bad direct-tcpip payload")
			continue
		}
		target, err := net.Dial("tcp", net.JoinHostPort(payload.Raddr, strconv.Itoa(int(payload.Rport))))
		if err != nil {
			newChan.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			target.Close()
			continue
		}
		go ssh.DiscardRequests(chReqs)
		go func() {
			defer ch.Close()
			defer target.Close()
			io.Copy(ch, target)
		}()
		go func() {
			defer ch.Close()
			defer target.Close()
			io.Copy(target, ch)
		}()
	}
}

// startEchoTarget 启动一个回显 TCP 服务（写什么读回什么），返回端口号。
func startEchoTarget(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// tunnelTestCfg 构造指向 in-process SSH 服务器的隧道配置。
func tunnelTestCfg(t *testing.T, sshAddr string, remotePort int, localPort int) *config.SSHConfig {
	t.Helper()
	sshHost, sshPortStr, err := net.SplitHostPort(sshAddr)
	assert.NoError(t, err)
	sshPort, err := strconv.Atoi(sshPortStr)
	assert.NoError(t, err)
	return &config.SSHConfig{
		Enable:                true,
		Host:                  sshHost,
		Port:                  sshPort,
		User:                  "u",
		KeyPath:               writeTestKey(t),
		RemoteHost:            "127.0.0.1",
		RemotePort:            remotePort,
		LocalPort:             localPort,
		InsecureIgnoreHostKey: true, // 测试服务器 host key 不在 known_hosts
	}
}

// TestEstablishTunnelEphemeralLocalPort 验证 A6：未配置 local_port（0）时由
// 内核分配临时端口（而非固定 3330），establishTunnel 返回实际端口，数据能
// 穿透隧道到达远端。
func TestEstablishTunnelEphemeralLocalPort(t *testing.T) {
	sshAddr := startTestSSHServer(t)
	echoPort := startEchoTarget(t)
	cfg := tunnelTestCfg(t, sshAddr, echoPort, 0)

	host, port, closer, err := establishTunnel(cfg, 5)
	assert.NoError(t, err)
	defer closer.Close()
	assert.Equal(t, "127.0.0.1", host)
	assert.NotEqual(t, 3330, port)
	assert.Greater(t, port, 0)

	conn, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	assert.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("ping"))
	assert.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	assert.NoError(t, err)
	assert.Equal(t, "ping", string(buf))
}

// TestEstablishTunnelTwoConcurrentEphemeralPorts 验证 A6：两个并发实例都未
// 配置 local_port 时不再因固定 3330 端口冲突而第二个失败。
func TestEstablishTunnelTwoConcurrentEphemeralPorts(t *testing.T) {
	sshAddr := startTestSSHServer(t)
	echoPort := startEchoTarget(t)

	_, _, closer1, err := establishTunnel(tunnelTestCfg(t, sshAddr, echoPort, 0), 5)
	assert.NoError(t, err)
	defer closer1.Close()

	_, port2, closer2, err := establishTunnel(tunnelTestCfg(t, sshAddr, echoPort, 0), 5)
	assert.NoError(t, err)
	defer closer2.Close()
	assert.Greater(t, port2, 0)
}

// TestEstablishTunnelExplicitLocalPortKept 验证 A6 对照：显式配置的
// local_port 行为不变（监听并返回指定端口）。
func TestEstablishTunnelExplicitLocalPortKept(t *testing.T) {
	sshAddr := startTestSSHServer(t)
	echoPort := startEchoTarget(t)
	// 先占一个临时端口再释放，用作显式 local_port（同测试进程内无并发竞争）。
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	explicitPort := reserve.Addr().(*net.TCPAddr).Port
	reserve.Close()

	_, port, closer, err := establishTunnel(tunnelTestCfg(t, sshAddr, echoPort, explicitPort), 5)
	assert.NoError(t, err)
	defer closer.Close()
	assert.Equal(t, explicitPort, port)
}

// TestEstablishTunnelRemoteDialFailureContinues 验证 A5：远端目标不可达时，
// accept 循环关闭该条本地连接并继续服务后续连接（第二次拨号也被处理），
// 而不是挂死在第一条失败上。
func TestEstablishTunnelRemoteDialFailureContinues(t *testing.T) {
	sshAddr := startTestSSHServer(t)
	// 一个确定没有监听的端口：SSH 服务端拨号失败 -> 通道被拒 ->
	// client.DialContext 返回错误 -> 循环 close(local) 后 continue。
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	deadPort := reserve.Addr().(*net.TCPAddr).Port
	reserve.Close()

	host, port, closer, err := establishTunnel(tunnelTestCfg(t, sshAddr, deadPort, 0), 5)
	assert.NoError(t, err)
	defer closer.Close()

	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		assert.NoError(t, err)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		// 循环应关闭该连接（EOF），而非让它挂到读超时。
		assert.ErrorIs(t, err, io.EOF, "conn %d should be closed by the accept loop", i)
		conn.Close()
	}
}
