package sshcheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type ErrorKind string

const (
	ErrorNetwork        ErrorKind = "network"
	ErrorTimeout        ErrorKind = "timeout"
	ErrorKnownHosts     ErrorKind = "known_hosts"
	ErrorHostKey        ErrorKind = "host_key"
	ErrorAuthentication ErrorKind = "authentication"
	ErrorHandshake      ErrorKind = "handshake"
	ErrorCommand        ErrorKind = "command"
	ErrorPrivateKey     ErrorKind = "private_key"
)

type CheckError struct {
	Kind ErrorKind
	Err  error
}

func (e *CheckError) Error() string { return e.Err.Error() }
func (e *CheckError) Unwrap() error { return e.Err }

type Request struct {
	Address        string
	User           string
	KnownHostsPath string
	PrivateKeyPath string
	Password       string
}

type Checker struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func New() *Checker {
	return &Checker{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
}

func DefaultKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func (c *Checker) Check(ctx context.Context, request Request) error {
	callback, err := knownhosts.New(request.KnownHostsPath)
	if err != nil {
		return &CheckError{Kind: ErrorKnownHosts, Err: fmt.Errorf("load known_hosts: %w", err)}
	}
	auth, err := authenticationMethods(request)
	if err != nil {
		return err
	}
	dial := c.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	connection, err := dial(ctx, "tcp", request.Address)
	if err != nil {
		kind := ErrorNetwork
		if timedOut(ctx, err) {
			kind = ErrorTimeout
		}
		return &CheckError{Kind: kind, Err: fmt.Errorf("dial SSH endpoint: %w", err)}
	}
	defer connection.Close()

	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return &CheckError{Kind: ErrorNetwork, Err: fmt.Errorf("set SSH deadline: %w", err)}
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancel()

	clientConnection, channels, requests, err := ssh.NewClientConn(connection, request.Address, &ssh.ClientConfig{
		User:            request.User,
		Auth:            auth,
		HostKeyCallback: callback,
	})
	if err != nil {
		return classifyHandshakeError(ctx, err)
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		if timedOut(ctx, err) {
			return &CheckError{Kind: ErrorTimeout, Err: fmt.Errorf("open SSH session: %w", err)}
		}
		return &CheckError{Kind: ErrorHandshake, Err: fmt.Errorf("open SSH session: %w", err)}
	}
	defer session.Close()
	if err := session.Run("true"); err != nil {
		if timedOut(ctx, err) {
			return &CheckError{Kind: ErrorTimeout, Err: fmt.Errorf("run SSH check command: %w", err)}
		}
		return &CheckError{Kind: ErrorCommand, Err: fmt.Errorf("run SSH check command: %w", err)}
	}
	return nil
}

func authenticationMethods(request Request) ([]ssh.AuthMethod, error) {
	methods := make([]ssh.AuthMethod, 0, 2)
	if request.PrivateKeyPath != "" {
		content, err := os.ReadFile(request.PrivateKeyPath)
		if err != nil {
			return nil, &CheckError{Kind: ErrorPrivateKey, Err: fmt.Errorf("read SSH private key: %w", err)}
		}
		signer, err := ssh.ParsePrivateKey(content)
		if err != nil {
			return nil, &CheckError{Kind: ErrorPrivateKey, Err: fmt.Errorf("parse SSH private key: %w", err)}
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if request.Password != "" {
		methods = append(methods, ssh.Password(request.Password))
	}
	if len(methods) == 0 {
		return nil, &CheckError{Kind: ErrorAuthentication, Err: errors.New("SSH credential is required")}
	}
	return methods, nil
}

func classifyHandshakeError(ctx context.Context, err error) error {
	if timedOut(ctx, err) {
		return &CheckError{Kind: ErrorTimeout, Err: fmt.Errorf("SSH handshake: %w", err)}
	}
	var keyError *knownhosts.KeyError
	if errors.As(err, &keyError) {
		return &CheckError{Kind: ErrorHostKey, Err: fmt.Errorf("SSH host key: %w", err)}
	}
	if strings.Contains(strings.ToLower(err.Error()), "unable to authenticate") {
		return &CheckError{Kind: ErrorAuthentication, Err: fmt.Errorf("SSH authentication: %w", err)}
	}
	return &CheckError{Kind: ErrorHandshake, Err: fmt.Errorf("SSH handshake: %w", err)}
}

func timedOut(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
