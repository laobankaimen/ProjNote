package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"projnote/internal/backup"
	"projnote/internal/files"
	"projnote/internal/platform"
	"projnote/internal/server"
	"projnote/internal/store"
)

var version = "0.1.0-dev"

type runtimeInfo struct {
	URL      string `json:"url"`
	Instance string `json:"instance"`
	PID      int    `json:"pid"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ProjNote:", err)
		os.Exit(1)
	}
}
func defaultDataDir() string {
	exe, err := os.Executable()
	if err == nil {
		base := filepath.Dir(exe)
		f, e := os.CreateTemp(base, ".projnote-write-")
		if e == nil {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return filepath.Join(base, "data")
		}
	}
	if config, e := os.UserConfigDir(); e == nil {
		return filepath.Join(config, "ProjNote", "data")
	}
	return "data"
}
func run() error {
	dataFlag := flag.String("data-dir", "", "数据目录（默认程序同级 data，可写性不足时使用用户配置目录）")
	port := flag.Int("port", 18600, "首选本地端口，冲突时顺延最多 99 个端口")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	restore := flag.String("restore", "", "停止服务后，从完整 ZIP 恢复；自动创建当前数据回滚备份")
	verify := flag.String("verify-backup", "", "校验完整 ZIP 的全部文件哈希与数据库，不覆盖数据")
	stop := flag.Bool("stop", false, "结束此数据目录正在运行的服务")
	flag.Parse()
	if *verify != "" {
		parent, err := os.MkdirTemp("", "projnote-verify-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(parent)
		stage, m, err := backup.Extract(*verify, parent)
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err = validateSnapshot(stage); err != nil {
			return err
		}
		fmt.Printf("备份校验通过：%d 个文件，生成时间 %s\n", len(m.Files), m.CreatedAt)
		return nil
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	if *stop {
		return stopExisting(dataDir)
	}
	lock, err := platform.AcquireLock(filepath.Join(dataDir, "app.lock"))
	if err != nil {
		if *restore != "" {
			return errors.New("数据目录正在使用或无法加锁；请先退出服务再恢复")
		}
		info, e := existing(dataDir)
		if e != nil {
			return fmt.Errorf("无法锁定数据目录，且未发现可验证的运行实例: %w", err)
		}
		fmt.Println("ProjNote 已在运行：", info.URL)
		if !*noBrowser {
			return platform.OpenBrowser(info.URL)
		}
		return nil
	}
	defer lock.Close()
	if err = backup.CheckInterrupted(dataDir); err != nil {
		return err
	}
	if *restore != "" {
		return restoreOffline(dataDir, *restore)
	}
	logDir := filepath.Join(dataDir, "logs")
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "app-"+time.Now().Format("20060102")+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logFile, nil)))
	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if *port < 1024 || *port > 65436 {
		return errors.New("端口需介于 1024 和 65436 之间")
	}
	var listener net.Listener
	for candidate := *port; candidate < *port+100; candidate++ {
		listener, err = net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate)))
		if err == nil {
			break
		}
	}
	if listener == nil {
		return fmt.Errorf("没有可用端口: %w", err)
	}
	defer listener.Close()
	host := listener.Addr().String()
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return err
	}
	info := runtimeInfo{URL: server.URL(host), Instance: hex.EncodeToString(b), PID: os.Getpid()}
	runtimePath := filepath.Join(dataDir, "runtime.json")
	raw, _ := json.Marshal(info)
	if err = os.WriteFile(runtimePath, raw, 0600); err != nil {
		return err
	}
	defer os.Remove(runtimePath)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	app := &server.Server{Store: st, DataDir: dataDir, Host: host, Version: version, RuntimeToken: info.Instance, Shutdown: cancel}
	srv.Handler = app.Handler()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ensureDaily(st, dataDir)
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ensureDaily(st, dataDir)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
	}()
	fmt.Printf("ProjNote %s\n本机地址：%s\n数据目录：%s\n关闭浏览器后服务仍运行，可在设置中退出或使用 --stop。\n", version, info.URL, dataDir)
	if !*noBrowser {
		if e := platform.OpenBrowser(info.URL); e != nil {
			slog.Warn("browser unavailable", "error", e)
			fmt.Println("请手动打开上面的本机地址。")
		}
	}
	err = srv.Serve(listener)
	cancel()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func existing(root string) (runtimeInfo, error) {
	var info runtimeInfo
	b, err := os.ReadFile(filepath.Join(root, "runtime.json"))
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(b, &info); err != nil {
		return info, err
	}
	u, err := url.Parse(info.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return info, errors.New("实例地址无效")
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("不允许重定向") }}
	resp, err := client.Get(info.URL + "/api/health")
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	var health struct {
		Data struct {
			Instance string `json:"instance"`
		} `json:"data"`
	}
	if resp.StatusCode != 200 {
		return info, errors.New("实例状态异常")
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health); err != nil {
		return info, err
	}
	if info.Instance == "" || health.Data.Instance != info.Instance {
		return info, errors.New("端口上的服务不是原 ProjNote 实例")
	}
	return info, nil
}
func stopExisting(root string) error {
	info, err := existing(root)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", info.URL+"/api/system/shutdown", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-ProjNote-Request", "1")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("服务未能退出")
	}
	fmt.Println("已通知服务退出。")
	return nil
}
func ensureDaily(st *store.Store, root string) {
	list, err := backup.List(root)
	if err != nil {
		slog.Error("automatic backup list failed", "error", err)
		return
	}
	today := time.Now().Format("2006-01-02")
	if len(list) > 0 {
		if at, e := time.Parse(time.RFC3339, list[0].CreatedAt); e == nil && at.Local().Format("2006-01-02") == today {
			return
		}
	}
	st.Mu.Lock()
	info, err := backup.Create(root, st.DB)
	st.Mu.Unlock()
	if err != nil {
		slog.Error("automatic backup failed", "error", err)
	} else {
		slog.Info("automatic backup created", "name", info.Name)
		if e := st.RecordEvent("backup", "system", info.Name, map[string]any{"size": info.Size, "automatic": true}); e != nil {
			slog.Error("backup audit failed", "error", e)
		}
	}
}
func restoreOffline(root, archive string) error {
	stage, m, err := backup.Extract(archive, root)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = validateSnapshot(stage); err != nil {
		return err
	}
	if _, e := os.Stat(filepath.Join(root, "app.db")); e == nil {
		st, err := store.Open(root)
		if err != nil {
			return fmt.Errorf("当前数据库无法打开；已保留原数据，请先复制数据目录到新目录再恢复: %w", err)
		}
		info, e := backup.Create(root, st.DB)
		closeErr := st.Close()
		if e != nil {
			return fmt.Errorf("创建回滚备份失败: %w", e)
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Println("回滚备份：", filepath.Join(root, "backups", info.Name))
	}
	if err = backup.Install(root, stage); err != nil {
		return err
	}
	restored, err := store.Open(root)
	if err != nil {
		return err
	}
	err = restored.RecordEvent("restore", "system", m.CreatedAt, map[string]any{"backup_created_at": m.CreatedAt, "files": len(m.Files)})
	closeErr := restored.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("恢复完成：%d 个文件，备份时间 %s。请重新启动 ProjNote。\n", len(m.Files), m.CreatedAt)
	return nil
}

func validateSnapshot(root string) error {
	st, err := store.Open(root)
	if err != nil {
		return err
	}
	defer st.Close()
	result, err := st.VerifyAudit()
	if err != nil {
		return err
	}
	if result["ok"] != true {
		return fmt.Errorf("备份审计链校验失败: %v", result["broken_at"])
	}
	return files.CheckReferences(root, st.DB)
}
