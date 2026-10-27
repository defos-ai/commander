package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	serviceAccountCA    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	serviceAccountToken = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type accountFileReader func(string, int64) ([]byte, error)

// NewInClusterClient authenticates as the Pod's ServiceAccount. Kubernetes owns
// the endpoint environment and projected credential files; no auth settings,
// kubeconfig, home credentials or environment proxies are discovered.
func NewInClusterClient(namespace string) (*Client, error) {
	server, err := inClusterServer(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"))
	if err != nil {
		return nil, err
	}
	return newInClusterClient(namespace, server, readAccountFile, nil)
}

func newInClusterClient(namespace, server string, read accountFileReader, dial func(context.Context, string, string) (net.Conn, error)) (*Client, error) {
	transport := &http.Transport{DialContext: dial, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 90 * time.Second}
	if transport.DialContext == nil {
		transport.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	client, err := NewClient(namespace, server, &http.Client{Transport: transport})
	if err != nil {
		return nil, err
	}
	client.closeTransport = transport.CloseIdleConnections
	ca, err := read(serviceAccountCA, 1<<20)
	if err != nil {
		_ = client.Close()
		return nil, errors.New("read Kubernetes service account CA file")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		_ = client.Close()
		return nil, errors.New("Kubernetes service account CA contains no valid certificates")
	}
	auth := &serviceAccountTransport{transport: transport, read: read}
	if _, err := auth.token(); err != nil {
		_ = client.Close()
		return nil, err
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	client.http.Transport = auth
	return client, nil
}

func inClusterServer(host, port string) (string, error) {
	invalid := errors.New("Kubernetes service host and numeric port are required for in-cluster authentication")
	if host == "" || port == "" {
		return "", invalid
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return "", invalid
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", invalid
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		if net.ParseIP(host) == nil || !strings.Contains(host, ":") {
			return "", invalid
		}
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return "", invalid
		}
		for _, label := range strings.Split(strings.ToLower(host), ".") {
			if len(label) > 63 || !podDNSLabelPattern.MatchString(label) {
				return "", invalid
			}
		}
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(number)), nil
}

type serviceAccountTransport struct {
	transport *http.Transport
	read      accountFileReader
}

func (t *serviceAccountTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	// Projected ServiceAccount tokens rotate independently of client lifetime.
	token, err := t.token()
	if err != nil {
		return nil, err
	}
	authenticated := request.Clone(request.Context())
	authenticated.Header.Set("Authorization", "Bearer "+token)
	return t.transport.RoundTrip(authenticated)
}

func (t *serviceAccountTransport) token() (string, error) {
	data, err := t.read(serviceAccountToken, 64<<10)
	if err != nil {
		return "", errors.New("read Kubernetes service account token file")
	}
	defer clear(data)
	token := bytes.TrimSuffix(data, []byte("\n"))
	if len(token) < len(data) {
		token = bytes.TrimSuffix(token, []byte("\r"))
	}
	valid, padding := len(token) > 0, false
	for i, char := range token {
		if char == '=' {
			valid = valid && i > 0
			padding = true
			continue
		}
		valid = valid && !padding && (char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~+/", rune(char)))
	}
	if !valid {
		return "", errors.New("Kubernetes service account token is empty or contains invalid bearer characters")
	}
	return string(token), nil
}

func readAccountFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open service account file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid service account file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, errors.New("read service account file")
	}
	return data, nil
}
