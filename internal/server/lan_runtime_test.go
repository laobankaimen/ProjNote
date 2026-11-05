package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lanTestPort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		if port >= 1024 && port <= 65436 {
			return port
		}
	}
	t.Fatal("no test LAN port")
	return 0
}
func trustedLANClient(t *testing.T, root string) *http.Client {
	t.Helper()
	certificate, err := os.ReadFile(filepath.Join(root, "lan", "server.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid public LAN certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}
func lanTestResponse(t *testing.T, client *http.Client, url string, want int) *http.Response {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("GET %s: %d %s", url, response.StatusCode, raw)
	}
	return response
}
func TestLANRuntimeTrustedClientsSeparateOwnerSurfaceStopAndRestart(t *testing.T) {
	s := testServer(t)
	var revocations atomic.Int32
	lan := NewLANRuntime(s.DataDir, "http://"+s.Host, s.LANHandler(), func() error { revocations.Add(1); return s.Store.RevokeAllLANSessions() })
	s.StartLAN, s.StopLAN, s.LANInfo = lan.Start, lan.Stop, lan.Info
	t.Cleanup(func() { _ = lan.Close() })
	if lan.Info()["running"] != false {
		t.Fatal("LAN enabled by construction")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "lan")); !os.IsNotExist(err) {
		t.Fatal("disabled LAN generated an identity", err)
	}
	port := lanTestPort(t)
	info, err := lan.Start(port)
	if err != nil {
		t.Fatal(err)
	}
	if info["running"] != true || info["local_owner_url"] != "http://"+s.Host || info["certificate_download"] != "/api/lan/certificate" || revocations.Load() != 1 {
		t.Fatal(info, revocations.Load())
	}
	certificate, err := os.ReadFile(filepath.Join(s.DataDir, "lan", "server.crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certificate)
	fingerprint := sha256.Sum256(block.Bytes)
	if info["certificate_sha256"] != hex.EncodeToString(fingerprint[:]) {
		t.Fatal("fingerprint is not the public DER certificate digest", info)
	}
	url := fmt.Sprintf("https://127.0.0.1:%d", port)
	for _, client := range []*http.Client{trustedLANClient(t, s.DataDir), trustedLANClient(t, s.DataDir)} {
		response := lanTestResponse(t, client, url+"/api/health", 200)
		if response.TLS == nil || response.TLS.Version < tls.VersionTLS12 {
			t.Fatal("missing strong TLS")
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if bytes.Contains(raw, []byte("instance")) || bytes.Contains(raw, []byte("RuntimeToken")) {
			t.Fatal("remote health exposed owner instance", string(raw))
		}
		response = lanTestResponse(t, client, url+"/api/projects", 403)
		response.Body.Close()
		response = lanTestResponse(t, client, url+"/api/lan/workspace", 401)
		response.Body.Close()
	}
	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}}, Timeout: time.Second}
	if response, err := untrusted.Get(url + "/api/health"); err == nil {
		response.Body.Close()
		t.Fatal("untrusted client accepted certificate")
	}
	oldTLS := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS10, RootCAs: x509.NewCertPool()}}, Timeout: time.Second}
	if response, err := oldTLS.Get(url + "/api/health"); err == nil {
		response.Body.Close()
		t.Fatal("TLS1.0 accepted")
	}
	if _, err = lan.Start(port); err != nil || revocations.Load() != 1 {
		t.Fatal("same-port start revoked sessions or recreated listener", err, revocations.Load())
	}
	busy, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	if busyPort <= 65436 {
		if _, err = lan.Start(busyPort); err == nil {
			t.Fatal("port conflict accepted")
		}
		if lan.Info()["port"] != port || lan.Info()["running"] != true || revocations.Load() != 1 {
			t.Fatal("port conflict disrupted old runtime", lan.Info())
		}
	}
	client := trustedLANClient(t, s.DataDir)
	response := lanTestResponse(t, client, url+"/api/health", 200)
	response.Body.Close()
	member, err := s.Store.CreateLANMember(map[string]any{"name": "真实会话成员"})
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := s.Store.CreateLANPairing(fmt.Sprint(member["id"]), map[string]any{"version": member["version"]})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Store.ExchangeLANPairing(fmt.Sprint(pairing["code"]), "runtime-test")
	if err != nil {
		t.Fatal(err)
	}
	pendingPair, err := s.Store.CreateLANPairing(fmt.Sprint(member["id"]), map[string]any{"version": member["version"]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.LANSession(fmt.Sprint(session["token"])); err != nil {
		t.Fatal(err)
	}
	if err = lan.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.LANSession(fmt.Sprint(session["token"])); err == nil {
		t.Fatal("stop retained a real authenticated session")
	}
	if _, err := s.Store.ExchangeLANPairing(fmt.Sprint(pendingPair["code"]), "old-pairing"); err == nil {
		t.Fatal("stop retained an unconsumed pairing code")
	}
	if lan.Info()["running"] != false || revocations.Load() != 2 {
		t.Fatal("stop did not revoke", lan.Info(), revocations.Load())
	}
	if response, err := client.Get(url + "/api/health"); err == nil {
		response.Body.Close()
		t.Fatal("stopped listener still accepted requests")
	}
	if got := call(s, "GET", "/api/health", "", nil); got.Code != 200 {
		t.Fatal("LAN stop closed primary owner service", got.Code)
	}
	restarted, err := lan.Start(port)
	if err != nil {
		t.Fatal(err)
	}
	if restarted["certificate_sha256"] != info["certificate_sha256"] || revocations.Load() != 3 {
		t.Fatal("restart replaced certificate or kept old sessions", restarted)
	}
	response = lanTestResponse(t, client, url+"/api/health", 200)
	response.Body.Close()
	if err = lan.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = lan.Start(port); err == nil {
		t.Fatal("main shutdown allowed listener to restart")
	}
}
func TestLANRuntimeCertificatePersistencePermissionsExplicitRenewal(t *testing.T) {
	root := t.TempDir()
	ips := []net.IP{net.IPv4(127, 0, 0, 1).To4(), net.IPv4(192, 168, 3, 8).To4()}
	at := time.Now()
	_, leaf, err := loadLANCertificate(root, ips, at)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.VerifyHostname("localhost") != nil || leaf.VerifyHostname("192.168.3.8") != nil || !leaf.IsCA {
		t.Fatal("certificate cannot be explicitly trusted", leaf)
	}
	first, err := os.ReadFile(filepath.Join(root, "lan", "server.crt"))
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := loadLANCertificate(root, ips, at)
	if err != nil || !bytes.Equal(again.Raw, leaf.Raw) {
		t.Fatal("ordinary restart changed trust identity", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, "lan", "server.key"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private key permissions", info, err)
		}
	}
	changed := append(append([]net.IP{}, ips...), net.IPv4(192, 168, 3, 9).To4())
	if _, _, err = loadLANCertificate(root, changed, at); err == nil {
		t.Fatal("address change silently reissued identity")
	}
	if _, _, err = loadLANCertificate(root, ips, at.AddDate(4, 0, 0)); err == nil {
		t.Fatal("expired certificate accepted")
	}
	var revoked atomic.Int32
	lan := NewLANRuntime(root, "http://127.0.0.1:18600", http.NotFoundHandler(), func() error { revoked.Add(1); return nil })
	info, err := lan.RenewCertificate()
	if err != nil || info["certificate_renewed"] != true || revoked.Load() != 1 {
		t.Fatal(info, err)
	}
	second, err := os.ReadFile(filepath.Join(root, "lan", "server.crt"))
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("explicit renewal did not change certificate", err)
	}
	if _, err = lan.Start(lanTestPort(t)); err != nil {
		t.Fatal("renewed certificate unusable", err)
	}
	defer lan.Close()
	if _, err = lan.RenewCertificate(); err == nil {
		t.Fatal("live runtime certificate was replaced")
	}
}
func TestLANRuntimeRevocationFailureAndInvalidIdentityFailClosed(t *testing.T) {
	root := t.TempDir()
	ips := []net.IP{net.IPv4(127, 0, 0, 1).To4()}
	lan := NewLANRuntime(root, "http://127.0.0.1:18600", http.NotFoundHandler(), func() error { return errors.New("test revoke failure") })
	port := lanTestPort(t)
	if info, err := lan.Start(port); err == nil || info["running"] != false {
		t.Fatal("revocation failure exposed listener", info, err)
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatal("failed start left a bound port", err)
	}
	listener.Close()
	if _, err = lan.RenewCertificate(); err == nil {
		t.Fatal("revocation failure still replaced identity")
	}
	if err = os.Remove(filepath.Join(root, "lan", "server.key")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadLANCertificate(root, ips, time.Now()); err == nil {
		t.Fatal("missing key silently changed identity")
	}
	if err = os.WriteFile(filepath.Join(root, "lan", "server.key"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadLANCertificate(root, ips, time.Now()); err == nil {
		t.Fatal("invalid key accepted")
	}
	outside := filepath.Join(t.TempDir(), "identity")
	if err = os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "lan", "server.key")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, "lan", "server.key")); err != nil {
		t.Log("symlink unavailable", err)
		return
	}
	if _, _, err = loadLANCertificate(root, ips, time.Now()); err == nil {
		t.Fatal("private key symlink followed")
	}
}
func TestLANRuntimeStopClosesExistingTLSConnection(t *testing.T) {
	var calls atomic.Int32
	lan := NewLANRuntime(t.TempDir(), "http://127.0.0.1:18600", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, "member") }), func() error { return nil })
	port := lanTestPort(t)
	if _, err := lan.Start(port); err != nil {
		t.Fatal(err)
	}
	defer lan.Close()
	client := trustedLANClient(t, lan.dataDir)
	response := lanTestResponse(t, client, fmt.Sprintf("https://127.0.0.1:%d", port), 200)
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err := lan.Stop(); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d", port)); err == nil {
		response.Body.Close()
		t.Fatal("keepalive client reused a stopped connection")
	}
	if calls.Load() != 1 {
		t.Fatal("stopped handler processed another operation", calls.Load())
	}
	info, _ := json.Marshal(lan.Info())
	if strings.Contains(string(info), "PRIVATE KEY") || strings.Contains(string(info), "server.key") {
		t.Fatal("runtime info exposed private material", string(info))
	}
}
