package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

const (
	releasePath = "/defos-ai/fixture-agent/releases/download/v0.1.0/"
	binaryPath  = "/fixtures/fixture-agent"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	binary, err := os.Open(binaryPath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, binary)
	closeErr := binary.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	asset := "fixture-agent_linux_" + runtime.GOARCH
	nativeManifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: v0.1.0\nnative:\n  linux/%s:\n    asset: %s\n    sha256: %q\n", runtime.GOARCH, asset, fmt.Sprintf("%x", hash.Sum(nil)))
	if _, err := manifest.Parse([]byte(nativeManifest), "v0.1.0"); err != nil {
		return err
	}
	certificate, err := createCertificate()
	if err != nil {
		return err
	}
	if err := writeMissions(); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/defos-ai/fixture-agent/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v0.1.0","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z"}`)
	})
	mux.HandleFunc("GET "+releasePath+"agent.yaml", func(w http.ResponseWriter, r *http.Request) {
		body := nativeManifest
		if registry := os.Getenv("FIXTURE_REGISTRY_URL"); registry != "" {
			image, err := registryImage(r.Context(), registry)
			if err != nil {
				http.Error(w, "fixture image unavailable", http.StatusBadGateway)
				return
			}
			body += fmt.Sprintf("docker:\n  image: %q\n", image)
		}
		if _, err := manifest.Parse([]byte(body), "v0.1.0"); err != nil {
			http.Error(w, "invalid fixture manifest", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, body)
	})
	mux.HandleFunc("GET "+releasePath+asset, func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, binaryPath)
	})
	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	registerGates(health)
	secureListener, err := net.Listen("tcp", ":443")
	if err != nil {
		return err
	}
	defer secureListener.Close()
	healthListener, err := net.Listen("tcp", ":8080")
	if err != nil {
		return err
	}
	defer healthListener.Close()
	secure := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	probe := &http.Server{Handler: health, ReadHeaderTimeout: 5 * time.Second}
	defer secure.Close()
	defer probe.Close()
	failed := make(chan error, 2)
	go func() {
		failed <- secure.Serve(tls.NewListener(secureListener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}))
	}()
	go func() { failed <- probe.Serve(healthListener) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-failed:
		return err
	}
}

func registryImage(ctx context.Context, registry string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(registry, "/")+"/v2/fixture-agent/manifests/fixture", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fixture registry returned HTTP %d", response.StatusCode)
	}
	image := os.Getenv("FIXTURE_IMAGE")
	if image == "" {
		return "", errors.New("FIXTURE_IMAGE is required with FIXTURE_REGISTRY_URL")
	}
	return image + "@" + response.Header.Get("Docker-Content-Digest"), nil
}

func writeMissions() error {
	var missions []json.RawMessage
	for _, path := range []string{"/fixtures/default-missions.json", "/fixtures/spawner-missions.json"} {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var definitions []json.RawMessage
		if err := json.Unmarshal(data, &definitions); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		missions = append(missions, definitions...)
	}
	data, err := json.Marshal(missions)
	if err != nil {
		return err
	}
	return os.WriteFile("/tls/missions.json", data, 0o644)
}

func createCertificate() (tls.Certificate, error) {
	caPublic, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Commander test release CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	serverPublic, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	server := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "github.com"}, DNSNames: []string{"github.com", "api.github.com"},
		NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, serverPublic, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll("/tls", 0o755); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile("/tls/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{serverDER, caDER}, PrivateKey: serverKey}, nil
}
