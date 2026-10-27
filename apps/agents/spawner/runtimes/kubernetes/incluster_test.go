package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const authPod = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"example-agent-12345678","namespace":"agents","uid":"original-uid","resourceVersion":"opaque-version"},"spec":{}}`

func authCertificate(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	root, err := x509.CreateCertificate(rand.Reader, ca, ca, public, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
		DNSNames: []string{"kubernetes.invalid"}, IPAddresses: []net.IP{net.ParseIP("fd00::1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	server, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root}), tls.Certificate{Certificate: [][]byte{server}, PrivateKey: key}
}

func authFiles(t *testing.T, ca []byte) (accountFileReader, string) {
	t.Helper()
	root := t.TempDir()
	caPath, tokenPath := filepath.Join(root, "ca.crt"), filepath.Join(root, "token")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	authWrite(t, tokenPath, "first.synthetic-token\n")
	return func(path string, limit int64) ([]byte, error) {
		switch path {
		case serviceAccountCA:
			return readAccountFile(caPath, limit)
		case serviceAccountToken:
			return readAccountFile(tokenPath, limit)
		default:
			t.Fatal("authentication attempted to read an unrelated file")
			return nil, errors.New("unexpected account path")
		}
	}, tokenPath
}

func authWrite(t *testing.T, path, token string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatal("write synthetic account fixture")
	}
}

type authPeer struct {
	dial     func(context.Context, string, string) (net.Conn, error)
	headers  chan http.Header
	address  chan string
	finished chan struct{}
	calls    atomic.Int64
}

// Real HTTPS over net.Pipe verifies TLS without listeners or network sockets.
func authTLS(t *testing.T, config *tls.Config, status int) *authPeer {
	t.Helper()
	client, server := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	peer := &authPeer{headers: make(chan http.Header, 8), address: make(chan string, 1), finished: make(chan struct{})}
	peer.dial = func(ctx context.Context, _, address string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if peer.calls.Add(1) != 1 {
			return nil, errors.New("unexpected repeated dial")
		}
		peer.address <- address
		return client, nil
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case <-peer.finished:
		case <-time.After(time.Second):
			t.Error("in-memory TLS peer did not stop")
		}
	})
	go func() {
		defer close(peer.finished)
		defer server.Close()
		connection := tls.Server(server, config)
		if connection.Handshake() != nil {
			return
		}
		reader := bufio.NewReader(connection)
		for {
			request, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			_ = request.Body.Close()
			peer.headers <- request.Header.Clone()
			response := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(authPod)), ContentLength: int64(len(authPod))}
			response.Header.Set("Content-Type", "application/json")
			response.Header.Set("Location", "https://other.invalid?token=private-auth-sentinel")
			if response.Write(connection) != nil {
				return
			}
		}
	}()
	return peer
}

func TestInClusterTokenRotationAndOwnedConnectionCleanup(t *testing.T) {
	ca, certificate := authCertificate(t)
	read, tokenPath := authFiles(t, ca)
	peer := authTLS(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}, 200)
	t.Setenv("HTTPS_PROXY", "http://private-auth-sentinel.invalid:3128")
	t.Setenv("KUBECONFIG", "/unused/private-auth-sentinel")
	client, err := newInClusterClient("agents", "https://kubernetes.invalid:443", read, peer.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, token := range []string{"first.synthetic-token", "rotated.synthetic-token"} {
		// Projected tokens change by atomic replacement, not client recreation.
		authWrite(t, tokenPath+".new", token+"\n")
		if err := os.Rename(tokenPath+".new", tokenPath); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Get(t.Context(), "example-agent-12345678"); err != nil {
			t.Fatal(err)
		}
		if header := <-peer.headers; header.Get("Authorization") != "Bearer "+token || header.Get("Proxy-Authorization") != "" {
			t.Fatal("request did not use only the current projected token")
		}
	}
	authWrite(t, tokenPath, "private-auth-sentinel\n\n")
	if _, err := client.Get(t.Context(), "example-agent-12345678"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private-auth-sentinel") {
		t.Fatalf("invalid rotation reused old credentials or disclosed them: %v", err)
	}
	authWrite(t, tokenPath, "restored.synthetic-token")
	if _, err := client.Get(t.Context(), "example-agent-12345678"); err != nil {
		t.Fatal("valid token rotation did not recover")
	}
	if (<-peer.headers).Get("Authorization") != "Bearer restored.synthetic-token" || peer.calls.Load() != 1 || <-peer.address != "kubernetes.invalid:443" {
		t.Fatal("authentication retained a stale token or inherited a proxy")
	}
	_ = client.Close()
	_ = client.Close()
	select {
	case <-peer.finished:
	case <-time.After(time.Second):
		t.Fatal("Close retained the owned idle TLS connection")
	}
}

func TestInClusterTLSVerificationAndRedirectIsolation(t *testing.T) {
	for _, mode := range []string{"untrusted CA", "wrong hostname", "obsolete TLS", "IPv6", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			ca, certificate := authCertificate(t)
			server, status := "https://kubernetes.invalid:443", 200
			config := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			switch mode {
			case "untrusted CA":
				ca, _ = authCertificate(t)
			case "wrong hostname":
				server = "https://other.invalid:443"
			case "obsolete TLS":
				config.MinVersion, config.MaxVersion = tls.VersionTLS10, tls.VersionTLS11
			case "IPv6":
				server = "https://[fd00::1]:443"
			case "redirect":
				status = 307
			}
			read, _ := authFiles(t, ca)
			peer := authTLS(t, config, status)
			client, err := newInClusterClient("agents", server, read, peer.dial)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Get(t.Context(), "example-agent-12345678")
			if mode == "IPv6" {
				if err != nil || <-peer.address != "[fd00::1]:443" {
					t.Fatalf("IPv6 endpoint failed verified TLS: %v", err)
				}
			} else if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private-auth-sentinel") || peer.calls.Load() != 1 {
				t.Fatalf("TLS verification/redirect isolation failed: %v", err)
			}
			if mode != "IPv6" && mode != "redirect" && len(peer.headers) != 0 {
				t.Fatal("credentials reached HTTP before certificate verification succeeded")
			}
		})
	}
}

func TestInClusterCredentialValidationAndCancellation(t *testing.T) {
	ca, _ := authCertificate(t)
	for _, mode := range []string{"missing CA", "invalid CA", "missing token", "empty token", "whitespace", "extra newline", "oversized token", "invalid namespace", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var reads int
			read := func(path string, limit int64) ([]byte, error) {
				reads++
				if path == serviceAccountCA {
					if mode == "missing CA" {
						return nil, errors.New("private-auth-sentinel: /private/account/path")
					}
					if mode == "invalid CA" {
						return []byte("private-auth-sentinel"), nil
					}
					return bytes.Clone(ca), nil
				}
				if path != serviceAccountToken {
					t.Fatal("authentication read an unrelated credential path")
				}
				if mode == "missing token" {
					return nil, errors.New("private-auth-sentinel: https://user:secret@host/")
				}
				value := map[string]string{"empty token": "", "whitespace": "private-auth-sentinel x", "extra newline": "private-auth-sentinel\n\n"}[mode]
				if mode == "canceled" {
					value = "synthetic.valid-token"
				}
				if mode == "oversized token" {
					value = strings.Repeat("x", int(limit)+1)
				}
				if int64(len(value)) > limit {
					return nil, errors.New("oversized synthetic file")
				}
				return []byte(value), nil // token() clears each caller-owned copy.
			}
			namespace := "agents"
			if mode == "invalid namespace" {
				namespace = "../agents"
			}
			client, err := newInClusterClient(namespace, "https://kubernetes.invalid", read, func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("invalid/canceled auth attempted a network connection")
				return nil, errors.New("unexpected dial")
			})
			if mode == "canceled" {
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				before := reads
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err = client.Get(ctx, "example-agent-12345678")
				if !errors.Is(err, context.Canceled) || reads != before {
					t.Fatal("canceled request read credentials or lost cancellation")
				}
			} else if err == nil || strings.Contains(err.Error(), "private-auth-sentinel") || strings.Contains(err.Error(), "/private/account/path") {
				if client != nil {
					_ = client.Close()
				}
				t.Fatalf("invalid auth was accepted or disclosed credential diagnostics: %v", err)
			}
			if mode == "invalid namespace" && reads != 0 {
				t.Fatal("invalid namespace read account files")
			}
		})
	}
}

func TestInClusterEndpointValidation(t *testing.T) {
	for _, host := range []string{"fd00::1", "[fd00::1]"} {
		if server, err := inClusterServer(host, "443"); err != nil || server != "https://[fd00::1]:443" {
			t.Fatal("in-cluster IPv6 address was not joined correctly")
		}
	}
	for _, test := range []struct{ host, port string }{{"", "443"}, {"host", ""}, {"host:443", "443"}, {"user:private-auth-sentinel@host", "443"}, {"host", "0"}, {"host", "65536"}, {"host", "https"}} {
		t.Setenv("KUBERNETES_SERVICE_HOST", test.host)
		t.Setenv("KUBERNETES_SERVICE_PORT", test.port)
		client, err := NewInClusterClient("agents")
		if err == nil {
			_ = client.Close()
			t.Fatal("invalid endpoint was accepted")
		}
		if !strings.Contains(err.Error(), "service host") || strings.Contains(err.Error(), "private-auth-sentinel") {
			t.Fatalf("endpoint validation read mounted files first or disclosed input: %v", err)
		}
	}
}
