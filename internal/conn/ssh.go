package conn

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"golang.org/x/crypto/ssh"
)

// tunnelHook is overridable for testing. The returned io.Closer releases the
// underlying SSH client and local listener so callers (e.g. Pool.Close) can
// tear the tunnel down cleanly.
type tunnelHook func(*config.SSHConfig) (host string, port int, closer io.Closer, err error)

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
func establishTunnel(cfg *config.SSHConfig) (string, int, io.Closer, error) {
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
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshHost, sshPort), &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:         10 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
	if localPort == 0 {
		localPort = 3330
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		client.Close()
		return "", 0, nil, fmt.Errorf("local listen: %w", err)
	}
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				log.Printf("ssh tunnel: listener closed: %v", err)
				return
			}
			remote, err := client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
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

func proxy(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}
