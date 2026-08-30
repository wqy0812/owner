package sshcheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestCheckerAcceptsPasswordAndRunsTrue(t *testing.T) {
	server := newTestSSHServer(t, testSSHServerOptions{password: "secret"})
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_rsa")
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	checker := New()
	if err := checker.Check(context.Background(), Request{
		Address: server.address, User: "root", Password: "secret", PrivateKeyPath: keyPath, KnownHostsPath: server.knownHostsPath,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckerAcceptsPrivateKey(t *testing.T) {
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	server := newTestSSHServer(t, testSSHServerOptions{publicKey: clientSigner.PublicKey()})
	keyPath := filepath.Join(t.TempDir(), "id_rsa")
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().Check(context.Background(), Request{
		Address: server.address, User: "root", PrivateKeyPath: keyPath, KnownHostsPath: server.knownHostsPath,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckerClassifiesHostKeyAuthenticationCommandAndPrivateKeyFailures(t *testing.T) {
	server := newTestSSHServer(t, testSSHServerOptions{password: "secret"})
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherSigner, err := ssh.NewSignerFromKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongKnownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(wrongKnownHosts, []byte(knownhosts.Line([]string{server.address}, otherSigner.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyKnownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(emptyKnownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		request Request
		kind    ErrorKind
	}{
		{name: "host key", request: Request{Address: server.address, User: "root", Password: "secret", KnownHostsPath: wrongKnownHosts}, kind: ErrorHostKey},
		{name: "unknown host", request: Request{Address: server.address, User: "root", Password: "secret", KnownHostsPath: emptyKnownHosts}, kind: ErrorHostKey},
		{name: "authentication", request: Request{Address: server.address, User: "root", Password: "wrong", KnownHostsPath: server.knownHostsPath}, kind: ErrorAuthentication},
		{name: "private key", request: Request{Address: server.address, User: "root", PrivateKeyPath: filepath.Join(t.TempDir(), "missing"), KnownHostsPath: server.knownHostsPath}, kind: ErrorPrivateKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCheckErrorKind(t, New().Check(context.Background(), test.request), test.kind)
		})
	}

	failingServer := newTestSSHServer(t, testSSHServerOptions{password: "secret", commandFails: true})
	assertCheckErrorKind(t, New().Check(context.Background(), Request{
		Address: failingServer.address, User: "root", Password: "secret", KnownHostsPath: failingServer.knownHostsPath,
	}), ErrorCommand)
}

func TestCheckerHonorsContextTimeoutDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			<-time.After(time.Second)
		}
	}()
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHostsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	assertCheckErrorKind(t, New().Check(ctx, Request{
		Address: listener.Addr().String(), User: "root", Password: "secret", KnownHostsPath: knownHostsPath,
	}), ErrorTimeout)
}

func TestCheckerClassifiesKnownHostsNetworkAndMissingCredentialFailures(t *testing.T) {
	missingKnownHosts := filepath.Join(t.TempDir(), "missing-known-hosts")
	assertCheckErrorKind(t, New().Check(context.Background(), Request{
		Address: "127.0.0.1:22", User: "root", Password: "secret", KnownHostsPath: missingKnownHosts,
	}), ErrorKnownHosts)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHostsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &Checker{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}}
	assertCheckErrorKind(t, checker.Check(context.Background(), Request{
		Address: "127.0.0.1:22", User: "root", Password: "secret", KnownHostsPath: knownHostsPath,
	}), ErrorNetwork)

	assertCheckErrorKind(t, New().Check(context.Background(), Request{
		Address: "127.0.0.1:22", User: "root", KnownHostsPath: knownHostsPath,
	}), ErrorAuthentication)
}

func TestCheckerRejectsMalformedPrivateKeyBeforeDial(t *testing.T) {
	root := t.TempDir()
	knownHostsPath := filepath.Join(root, "known_hosts")
	keyPath := filepath.Join(root, "id_rsa")
	if err := os.WriteFile(knownHostsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dialed := false
	checker := &Checker{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}}
	assertCheckErrorKind(t, checker.Check(context.Background(), Request{
		Address: "127.0.0.1:22", User: "root", PrivateKeyPath: keyPath, KnownHostsPath: knownHostsPath,
	}), ErrorPrivateKey)
	if dialed {
		t.Fatal("checker dialed before validating the private key")
	}
}

func assertCheckErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var checkErr *CheckError
	if !errors.As(err, &checkErr) || checkErr.Kind != kind {
		t.Fatalf("error=%v kind=%v want=%v", err, checkErr, kind)
	}
}

type testSSHServerOptions struct {
	password     string
	publicKey    ssh.PublicKey
	commandFails bool
}

type testSSHServer struct {
	address        string
	knownHostsPath string
}

func newTestSSHServer(t *testing.T, options testSSHServerOptions) testSSHServer {
	t.Helper()
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{}
	if options.password != "" {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == options.password {
				return nil, nil
			}
			return nil, errors.New("password rejected")
		}
	}
	if options.publicKey != nil {
		config.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), options.publicKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("key rejected")
		}
	}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveTestSSHConnection(connection, config, options.commandFails)
		}
	}()
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{listener.Addr().String()}, hostSigner.PublicKey()) + "\n"
	if err := os.WriteFile(knownHostsPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return testSSHServer{address: listener.Addr().String(), knownHostsPath: knownHostsPath}
}

func serveTestSSHConnection(connection net.Conn, config *ssh.ServerConfig, commandFails bool) {
	defer connection.Close()
	_, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range channelRequests {
				if request.Type != "exec" || len(request.Payload) < 4 {
					_ = request.Reply(false, nil)
					continue
				}
				length := int(binary.BigEndian.Uint32(request.Payload[:4]))
				command := ""
				if length <= len(request.Payload)-4 {
					command = string(request.Payload[4 : 4+length])
				}
				_ = request.Reply(true, nil)
				status := uint32(0)
				if command != "true" || commandFails {
					status = 1
				}
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}
