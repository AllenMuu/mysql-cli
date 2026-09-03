package conn

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// remoteDialTimeout 是 accept 循环内每条转发连接的远端拨号超时。SSH 半开
// 连接时远端拨号可能永久阻塞，必须用带超时的 DialContext 兜底，否则整个
// accept 循环 goroutine 会挂死且无法恢复。
const remoteDialTimeout = 30 * time.Second

// tunnelHook is overridable for testing. The returned io.Closer releases the
// underlying SSH client and local listener so callers (e.g. Pool.Close) can
// tear the tunnel down cleanly.
//
// connectTimeout 是 SSH 拨号超时（秒），<=0 时 establishTunnel 内部回落默认 10s。
// 通过参数显式传入而非让 establishTunnel 自行硬编码，是为了让 ds.ConnectTimeout
// 配置真正生效（SSHConfig 自身没有 ConnectTimeout 字段，不能改 config 包）。
type tunnelHook func(cfg *config.SSHConfig, connectTimeout int) (host string, port int, closer io.Closer, err error)

// defaultTunnelHook is the production tunnel hook.
var defaultTunnelHook tunnelHook = establishTunnel

// tunnel holds the resources backing an SSH port-forward so they can be
// released together. It implements io.Closer.
type tunnel struct {
	sshClient *ssh.Client
	listener  net.Listener
}

// Close stops accepting new forwarded connections and tears down the SSH
// client. Errors from each step are returned joined; a nil error means both
// steps succeeded.
func (t *tunnel) Close() error {
	var firstErr error
	if t.listener != nil {
		if err := t.listener.Close(); err != nil {
			firstErr = err
		}
	}
	if t.sshClient != nil {
		if err := t.sshClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// establishTunnel connects to the SSH bastion and forwards a local port
// to the remote MySQL. It returns the local address to dial and a Closer
// that releases the SSH client + local listener.
//
// connectTimeout 控制 ssh.Dial 的超时；<=0 时回落默认 10s（与历史行为一致）。
func establishTunnel(cfg *config.SSHConfig, connectTimeout int) (string, int, io.Closer, error) {
	// 先校验私钥文件权限：群组/其他可读的私钥拒绝使用，避免被同机其他用户窃取。
	// 必须在读私钥前做，否则 os.ReadFile 仍会成功读出内容（mode 只挡 open 不挡已读）。
	info, err := os.Stat(cfg.KeyPath)
	if err != nil {
		return "", 0, nil, fmt.Errorf("ssh key: %w", err)
	}
	if perm := info.Mode().Perm() & 0o077; perm != 0 {
		return "", 0, nil, fmt.Errorf("private key file %s is too open (mode %v); expected 0600 or stricter", cfg.KeyPath, info.Mode().Perm())
	}
	keyBytes, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return "", 0, nil, fmt.Errorf("ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return "", 0, nil, fmt.Errorf("parse ssh key: %w", err)
	}
	sshHost := cfg.Host
	if sshHost == "" {
		sshHost = "localhost"
	}
	sshPort := cfg.Port
	if sshPort == 0 {
		sshPort = 22
	}
	hostKeyCallback, err := buildHostKeyCallback(cfg)
	if err != nil {
		return "", 0, nil, err
	}
	// SSH 拨号超时从入参读取（来源 ds.ConnectTimeout），<=0 回落默认 10s。
	dialTimeout := time.Duration(connectTimeout) * time.Second
	if connectTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshHost, sshPort), &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:         dialTimeout,
		HostKeyCallback: hostKeyCallback,
	})
	if err != nil {
		return "", 0, nil, fmt.Errorf("ssh dial: %w", err)
	}
	remoteHost := cfg.RemoteHost
	if remoteHost == "" {
		remoteHost = "localhost"
	}
	remotePort := cfg.RemotePort
	if remotePort == 0 {
		remotePort = 3306
	}
	localPort := cfg.LocalPort
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		client.Close()
		return "", 0, nil, fmt.Errorf("local listen: %w", err)
	}
	if localPort == 0 {
		// 未显式配置 local_port：由内核分配临时端口。固定端口（历史默认
		// 3330）会让同一机器上第二个未配置端口的实例直接 address already
		// in use 失败；临时端口天然并发安全。显式配置的 local_port 行为不变。
		if addr, ok := listener.Addr().(*net.TCPAddr); ok {
			localPort = addr.Port
		}
	}
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				log.Printf("ssh tunnel: listener closed: %v", err)
				return
			}
			// 远端拨号必须带超时：client.Dial 内部用 context.Background()，
			// SSH 半开连接时会永久阻塞，把整个 accept 循环挂死。拨号失败只
			// 关闭该条本地连接并继续循环，不影响后续连接。
			dialCtx, cancel := context.WithTimeout(context.Background(), remoteDialTimeout)
			remote, err := client.DialContext(dialCtx, "tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
			cancel()
			if err != nil {
				log.Printf("ssh tunnel: remote dial %s:%d failed: %v", remoteHost, remotePort, err)
				local.Close()
				continue
			}
			go proxy(local, remote)
		}
	}()
	return "127.0.0.1", localPort, &tunnel{sshClient: client, listener: listener}, nil
}

// proxy 在两个 conn 之间双向 io.Copy。
//
// 等待两个方向的 goroutine 都结束才返回，避免 REPL 长期运行下另一个方向的
// goroutine 永久阻塞泄漏。为防止 tunnel.Close 后对端不主动关闭导致 io.Copy 卡住，
// 在第一个方向结束后启动 5s 宽限计时器：宽限内另一个方向仍未结束就强制关闭两侧
// conn（io.Copy 会在 conn 关闭后返回，不会真正泄漏 fd）。
//
// 注意：宽限计时器必须在「第一个方向结束」之后才启动，不能在 proxy 进入时立即启动
// ——否则长查询（>5s 的 SELECT/UPDATE）会被误杀，连接池空闲连接也会在 5s 后被
// 强制关闭导致下次复用拿到死连接。原实现即此 bug，已修复。
func proxy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	oneDone := make(chan struct{}, 2) // 缓冲 2，避免 goroutine 写入时阻塞
	go func() { defer wg.Done(); io.Copy(a, b); oneDone <- struct{}{} }()
	go func() { defer wg.Done(); io.Copy(b, a); oneDone <- struct{}{} }()
	// 等第一个方向结束。MySQL 协议下任一方向 EOF（server 关闭）通常意味着会话结束。
	<-oneDone
	select {
	case <-oneDone:
		// 第二个方向也结束，无需宽限。
	case <-time.After(5 * time.Second):
		// 宽限超时：另一个方向仍未结束，强制关闭两侧 conn 触发 io.Copy 返回。
		log.Printf("ssh tunnel: proxy grace timeout after 5s, forcing close")
	}
	a.Close()
	b.Close()
	wg.Wait()
}

// buildHostKeyCallback 构造 SSH host key 校验回调。
//
// 默认要求 known_hosts 校验（防 MITM）。仅当 cfg.InsecureIgnoreHostKey=true
// 时跳过校验，并在 stderr 打 warning。known_hosts 文件不存在时返回明确 error，
// 提示用户先 ssh 一次目标主机或显式开启 insecure 模式。
func buildHostKeyCallback(cfg *config.SSHConfig) (ssh.HostKeyCallback, error) {
	if cfg == nil {
		// 不允许 nil cfg 进 SSH 路径；调用方失误应立即暴露。
		return nil, fmt.Errorf("ssh config is nil, cannot build host key callback")
	}
	if cfg.InsecureIgnoreHostKey {
		fmt.Fprintln(os.Stderr, "WARNING: SSH host key verification disabled, MITM risk")
		return ssh.InsecureIgnoreHostKey(), nil
	}
	knownHostsFile := cfg.KnownHostsFile
	if knownHostsFile == "" {
		// 配置层 applyDefaults 应已填好默认值；此处兜底防止直连 SSH 配置未经 Resolve。
		if home, err := os.UserHomeDir(); err == nil {
			knownHostsFile = home + "/.ssh/known_hosts"
		}
	}
	if _, err := os.Stat(knownHostsFile); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("known_hosts file %q not found: ssh the target host once first, or set ssh.insecure_ignore_host_key=true (insecure, MITM risk)", knownHostsFile)
		}
		return nil, fmt.Errorf("stat known_hosts %q: %w", knownHostsFile, err)
	}
	cb, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %q: %w", knownHostsFile, err)
	}
	return cb, nil
}
