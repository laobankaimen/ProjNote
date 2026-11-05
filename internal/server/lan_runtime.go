package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const DefaultLANPort = 18680

// LANRuntime owns only the optional TLS listener. The primary loopback owner
// listener and runtime.json URL are deliberately outside this lifecycle.
// All starts/stops share mu; no Store lock may be held by their callers.
type LANRuntime struct {
	mu                                                    sync.Mutex
	dataDir, localOwnerURL                                string
	handler                                               http.Handler
	revoke                                                func() error
	server                                                *http.Server
	listener                                              net.Listener
	generation                                            uint64
	running, closing                                      bool
	port                                                  int
	certificateFingerprint, certificateExpires, lastError string
	addresses                                             []string
}

func NewLANRuntime(dataDir, localOwnerURL string, handler http.Handler, revoke func() error) *LANRuntime {
	return &LANRuntime{dataDir: dataDir, localOwnerURL: localOwnerURL, handler: handler, revoke: revoke, port: DefaultLANPort, addresses: []string{}}
}
func (runtime *LANRuntime) Info() map[string]any {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.infoLocked()
}
func (runtime *LANRuntime) infoLocked() map[string]any {
	addresses := append([]string{}, runtime.addresses...)
	info := map[string]any{"running": runtime.running, "enabled": runtime.running, "port": runtime.port, "addresses": addresses, "local_owner_url": runtime.localOwnerURL, "certificate_sha256": runtime.certificateFingerprint, "certificate_download": "/api/lan/certificate", "certificate_expires_at": runtime.certificateExpires}
	if runtime.lastError != "" {
		info["error"] = runtime.lastError
	}
	return info
}
func (runtime *LANRuntime) Start(port int) (map[string]any, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closing {
		return runtime.infoLocked(), errors.New("程序正在退出，不能启用局域网")
	}
	if port == 0 {
		port = DefaultLANPort
	}
	if port < 1024 || port > 65436 {
		return runtime.infoLocked(), errors.New("局域网端口需介于 1024 和 65436 之间")
	}
	if runtime.running && runtime.port == port {
		return runtime.infoLocked(), nil
	}
	if runtime.handler == nil || runtime.revoke == nil {
		return runtime.infoLocked(), errors.New("局域网身份服务尚未就绪")
	}
	ips, err := lanInterfaceIPs()
	if err != nil {
		return runtime.startErrorLocked(err)
	}
	certificate, leaf, err := loadLANCertificate(runtime.dataDir, ips, time.Now())
	if err != nil {
		return runtime.startErrorLocked(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		return runtime.startErrorLocked(fmt.Errorf("局域网端口 %d 无法监听: %w", port, err))
	}
	// Bind first: a conflict must not stop the old listener or invalidate its
	// sessions. New connections are not served until revocation has committed.
	if err = runtime.revoke(); err != nil {
		_ = listener.Close()
		return runtime.startErrorLocked(fmt.Errorf("旧局域网会话撤销失败: %w", err))
	}
	if runtime.server != nil {
		_ = runtime.server.Close()
	}
	if runtime.listener != nil {
		_ = runtime.listener.Close()
	}
	runtime.generation++
	generation := runtime.generation
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256}},
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtime.mu.Lock()
		active := runtime.running && !runtime.closing && runtime.generation == generation
		runtime.mu.Unlock()
		if !active {
			writeError(w, 503, "lan_disabled", "局域网服务已停止")
			return
		}
		runtime.handler.ServeHTTP(w, r)
	})
	tlsListener := tls.NewListener(listener, server.TLSConfig)
	runtime.server, runtime.listener, runtime.port = server, tlsListener, port
	runtime.running, runtime.lastError = true, ""
	digest := sha256.Sum256(leaf.Raw)
	runtime.certificateFingerprint = hex.EncodeToString(digest[:])
	runtime.certificateExpires = leaf.NotAfter.UTC().Format(time.RFC3339)
	runtime.addresses = lanAddresses(ips, port)
	go runtime.serve(server, tlsListener, generation)
	return runtime.infoLocked(), nil
}
func (runtime *LANRuntime) startErrorLocked(err error) (map[string]any, error) {
	runtime.lastError = err.Error()
	return runtime.infoLocked(), err
}
func (runtime *LANRuntime) serve(server *http.Server, listener net.Listener, generation uint64) {
	err := server.Serve(listener)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.generation != generation || runtime.server != server {
		return
	}
	runtime.running = false
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		runtime.lastError = "局域网监听意外结束，请在本机设置重新启用"
		slog.Error("LAN listener stopped", "error", err)
		if runtime.revoke != nil {
			if revokeErr := runtime.revoke(); revokeErr != nil {
				slog.Error("LAN stopped session revocation failed", "error", revokeErr)
			}
		}
	}
}
func (runtime *LANRuntime) Stop() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.stopLocked()
}
func (runtime *LANRuntime) stopLocked() error {
	active := runtime.running || runtime.server != nil || runtime.listener != nil
	runtime.running = false
	runtime.generation++
	var closeErr error
	if runtime.server != nil {
		if err := runtime.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			closeErr = err
		}
		runtime.server = nil
	}
	if runtime.listener != nil {
		_ = runtime.listener.Close()
		runtime.listener = nil
	}
	runtime.addresses = []string{}
	if active && runtime.revoke != nil {
		if err := runtime.revoke(); err != nil {
			closeErr = fmt.Errorf("局域网已关闭，但会话撤销未能保存: %w", err)
		}
	}
	if closeErr != nil {
		runtime.lastError = closeErr.Error()
		return closeErr
	}
	runtime.lastError = ""
	return nil
}

// Close permanently rejects restarts, including a simultaneous control request
// arriving while the primary owner service is shutting down.
func (runtime *LANRuntime) Close() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.closing = true
	return runtime.stopLocked()
}
func lanAddresses(ips []net.IP, port int) []string {
	addresses := []string{}
	for _, ip := range ips {
		if ip.IsLoopback() {
			continue
		}
		addresses = append(addresses, "https://"+net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	if len(addresses) == 0 {
		addresses = append(addresses, "https://127.0.0.1:"+strconv.Itoa(port))
	}
	return addresses
}
func lanInterfaceIPs() ([]net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("无法读取局域网网卡: %w", err)
	}
	unique := map[string]net.IP{"127.0.0.1": net.IPv4(127, 0, 0, 1).To4()}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP.To4()
			case *net.IPAddr:
				ip = value.IP.To4()
			}
			if ip != nil && !ip.IsUnspecified() && !ip.IsMulticast() {
				unique[ip.String()] = ip
			}
		}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ips := make([]net.IP, 0, len(keys))
	for _, key := range keys {
		ips = append(ips, unique[key])
	}
	return ips, nil
}
func loadLANCertificate(dataDir string, ips []net.IP, at time.Time) (tls.Certificate, *x509.Certificate, error) {
	dir := filepath.Join(dataDir, "lan")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return tls.Certificate{}, nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return tls.Certificate{}, nil, errors.New("局域网证书目录不是普通目录")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return tls.Certificate{}, nil, err
	}
	certificatePath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	certificatePEM, certErr := readLANIdentityFile(certificatePath)
	keyPEM, keyErr := readLANIdentityFile(keyPath)
	if os.IsNotExist(certErr) && os.IsNotExist(keyErr) {
		certificatePEM, keyPEM, err = generateLANCertificate(ips, at)
		if err != nil {
			return tls.Certificate{}, nil, err
		}
		if err = writeLANIdentityFile(keyPath, keyPEM, 0600); err != nil {
			return tls.Certificate{}, nil, err
		}
		if err = writeLANIdentityFile(certificatePath, certificatePEM, 0644); err != nil {
			_ = os.Remove(keyPath)
			return tls.Certificate{}, nil, err
		}
	} else if certErr != nil || keyErr != nil {
		return tls.Certificate{}, nil, errors.New("局域网证书或私钥缺失、无效或为符号链接；请在本机检查 lan/server.crt 与 lan/server.key，未自动替换身份")
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return tls.Certificate{}, nil, err
	}
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, errors.New("局域网证书与私钥不匹配，未自动替换身份")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if at.Before(leaf.NotBefore) || !at.Before(leaf.NotAfter) {
		return tls.Certificate{}, nil, errors.New("局域网证书已过期或尚未生效，请在本机更新证书并重新配对")
	}
	for _, ip := range ips {
		if err = leaf.VerifyHostname(ip.String()); err != nil {
			return tls.Certificate{}, nil, errors.New("网卡地址已变化，现有局域网证书未包含当前地址；请在本机更新证书后重新核对指纹")
		}
	}
	certificate.Leaf = leaf
	return certificate, leaf, nil
}
func readLANIdentityFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("局域网身份文件不是普通小文件")
	}
	return os.ReadFile(path)
}
func writeLANIdentityFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
func generateLANCertificate(ips []net.IP, at time.Time) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ProjNote LAN", Organization: []string{"ProjNote"}}, NotBefore: at.Add(-5 * time.Minute), NotAfter: at.AddDate(3, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"localhost"}, IPAddresses: ips}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// RenewCertificate is an explicit owner action while the listener is stopped.
// It intentionally changes the trust fingerprint and revokes every old pairing
// and session; ordinary starts never silently replace a trusted identity.
func (runtime *LANRuntime) RenewCertificate() (map[string]any, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closing {
		return runtime.infoLocked(), errors.New("程序正在退出，不能重新签发证书")
	}
	if runtime.running {
		return runtime.infoLocked(), errors.New("请先停止局域网协同，再重新签发证书并重新建立信任")
	}
	ips, err := lanInterfaceIPs()
	if err != nil {
		return runtime.startErrorLocked(err)
	}
	certificatePEM, keyPEM, err := generateLANCertificate(ips, time.Now())
	if err != nil {
		return runtime.startErrorLocked(err)
	}
	if runtime.revoke == nil {
		return runtime.startErrorLocked(errors.New("局域网身份服务尚未就绪"))
	}
	if err = runtime.revoke(); err != nil {
		return runtime.startErrorLocked(fmt.Errorf("旧会话撤销失败，证书未更换: %w", err))
	}
	if err = replaceLANIdentity(runtime.dataDir, certificatePEM, keyPEM); err != nil {
		return runtime.startErrorLocked(err)
	}
	block, _ := pem.Decode(certificatePEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return runtime.startErrorLocked(err)
	}
	digest := sha256.Sum256(leaf.Raw)
	runtime.certificateFingerprint = hex.EncodeToString(digest[:])
	runtime.certificateExpires = leaf.NotAfter.UTC().Format(time.RFC3339)
	runtime.lastError = ""
	info := runtime.infoLocked()
	info["certificate_renewed"] = true
	return info, nil
}
func replaceLANIdentity(dataDir string, certificatePEM, keyPEM []byte) error {
	dir := filepath.Join(dataDir, "lan")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("局域网证书目录不是普通目录")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	keyPath, certPath := filepath.Join(dir, "server.key"), filepath.Join(dir, "server.crt")
	oldKey, keyErr := readLANIdentityFile(keyPath)
	_, certErr := readLANIdentityFile(certPath)
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return keyErr
	}
	if certErr != nil && !os.IsNotExist(certErr) {
		return certErr
	}
	newKey, err := stageLANIdentityFile(dir, keyPEM, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(newKey)
	newCert, err := stageLANIdentityFile(dir, certificatePEM, 0644)
	if err != nil {
		return err
	}
	defer os.Remove(newCert)
	if err = os.Rename(newKey, keyPath); err != nil {
		return err
	}
	if err = os.Rename(newCert, certPath); err != nil {
		if os.IsNotExist(keyErr) {
			_ = os.Remove(keyPath)
		} else {
			rollback, writeErr := stageLANIdentityFile(dir, oldKey, 0600)
			if writeErr == nil {
				writeErr = os.Rename(rollback, keyPath)
				_ = os.Remove(rollback)
			}
			if writeErr != nil {
				return fmt.Errorf("证书更换失败，旧私钥恢复失败；请保持局域网关闭并检查本机证书: %w", writeErr)
			}
		}
		return fmt.Errorf("证书更换失败，已恢复旧私钥: %w", err)
	}
	return nil
}
func stageLANIdentityFile(dir string, data []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
