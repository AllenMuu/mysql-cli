package conn

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"golang.org/x/crypto/ssh"
)

// tunnelHook is overridable for testing.
type tunnelHook func(*config.SSHConfig) (host string, port int, err error)

// defaultTunnelHook is the production tunnel hook.
var defaultTunnelHook tunnelHook = func(cfg *config.SSHConfig) (string, int, error) {
	h, p, err := establishTunnel(cfg)
	return h, p, err
}

// establishTunnel connects to the SSH bastion and forwards a local port
// to the remote MySQL. It returns the local address to dial.
func establishTunnel(cfg *config.SSHConfig) (string, int, error) {
	keyBytes, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return "", 0, fmt.Errorf("ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return "", 0, fmt.Errorf("parse ssh key: %w", err)
	}
	sshHost := cfg.Host
	if sshHost == "" {
		sshHost = "localhost"
	}
	sshPort := cfg.Port
	if sshPort == 0 {
		sshPort = 22
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshHost, sshPort), &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		// TODO: configurable host-key verification
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return "", 0, fmt.Errorf("ssh dial: %w", err)
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
	if localPort == 0 {
		localPort = 3330
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		client.Close()
		return "", 0, fmt.Errorf("local listen: %w", err)
	}
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			remote, err := client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
			if err != nil {
				local.Close()
				continue
			}
			go proxy(local, remote)
		}
	}()
	return "127.0.0.1", localPort, nil
}

func proxy(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

// testCtx returns a context for tests (avoids importing context elsewhere).
func testCtx() context.Context { return context.Background() }
