package center_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jamiesun/scootship/internal/center"
	"github.com/jamiesun/scootship/internal/config"
	"github.com/jamiesun/scootship/internal/operators"
	"github.com/jamiesun/scootship/internal/store"
	"github.com/jamiesun/scootship/internal/tokens"
)

func TestRunServesDirectTLS(t *testing.T) {
	certPath, keyPath, roots := writeSelfSignedCert(t)
	addr := freeLocalAddr(t)
	srv := newRunTestServer(t, config.Config{
		Addr:    addr,
		TLSCert: certPath,
		TLSKey:  keyPath,
	})

	runServerForTest(t, srv)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 2 * time.Second,
	}
	assertHealthz(t, client, "https://"+addr+"/healthz")
}

func TestRunServesPlainHTTPOnlyForExplicitModes(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
	}{
		{name: "dev http", cfg: config.Config{Dev: true}},
		{name: "trusted proxy http", cfg: config.Config{BehindTLSProxy: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := freeLocalAddr(t)
			tt.cfg.Addr = addr
			srv := newRunTestServer(t, tt.cfg)
			runServerForTest(t, srv)
			assertHealthz(t, &http.Client{Timeout: 2 * time.Second}, "http://"+addr+"/healthz")
		})
	}
}

func newRunTestServer(t *testing.T, cfg config.Config) *center.Server {
	t.Helper()
	cfg.AdminUser = "admin"
	cfg.AdminPassword = "testpass"
	cfg.StaleSeconds = 90
	cfg.MaxTelemetryByte = 8 << 20
	cfg.AuditRetentionEvents = 1000

	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ops, err := operators.Open("", cfg.AdminUser, cfg.AdminPassword, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := center.New(cfg, st, tokens.New(map[string]string{"n-1": "secret"}), ops, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func runServerForTest(t *testing.T, srv *center.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("server did not shut down")
		}
	})
}

func assertHealthz(t *testing.T, client *http.Client, url string) {
	t.Helper()
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("healthz status=%d body=%s", resp.StatusCode, body)
			}
			if string(body) == "" {
				t.Fatal("empty healthz body")
			}
			return
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server did not become ready at %s: %v", url, lastErr)
}

func freeLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func writeSelfSignedCert(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add test certificate to root pool")
	}
	return certPath, keyPath, roots
}
