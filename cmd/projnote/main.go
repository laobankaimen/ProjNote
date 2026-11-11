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
	"strings"
	"sync"
	"syscall"
	"time"

	"projnote/internal/backup"
	"projnote/internal/files"
	"projnote/internal/platform"
	"projnote/internal/server"
	"projnote/internal/store"
)

var version = "0.5.1-dev"

type runtimeInfo struct {
	URL      string `json:"url"`
	Instance string `json:"instance"`
	PID      int    `json:"pid"`
}

func main() {
	err := prepareDesktopProcess()
	if err == nil {
		err = run()
	}
	desktopProcessDone(err)
	if err != nil {
		reportStartupFailure(err)
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
func run() (runErr error) {
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	dataFlag := flag.String("data-dir", "", "数据目录（默认程序同级 data，可写性不足时使用用户配置目录）")
	port := flag.Int("port", 18600, "首选本地端口，冲突时顺延最多 99 个端口")
	compact := flag.Bool("compact", false, "直接展开原生桌面便签（正常启动已默认显示贴边入口）")
	browser := flag.Bool("browser", false, "显式打开浏览器工作台（默认只显示桌面便签入口）")
	noBrowser := flag.Bool("no-browser", false, "仅启动服务，不自动打开浏览器和桌面入口")
	noDesktop := flag.Bool("no-desktop", false, "本次不显示桌面便签入口")
	restore := flag.String("restore", "", "停止服务后，从完整 ZIP 恢复；自动创建当前数据回滚备份")
	verify := flag.String("verify-backup", "", "校验完整 ZIP 的全部文件哈希与数据库，不覆盖数据")
	relocate := flag.String("relocate", "", "停服后复制数据到新的空目录，原目录保留")
	foreground := flag.Bool("foreground", false, "在前台运行服务（Linux 调试和终端运维）")
	lan := flag.Bool("lan", false, "显式启用独立 HTTPS 局域网协同（默认关闭；设置可保存启停状态）")
	lanPort := flag.Int("lan-port", server.DefaultLANPort, "局域网 HTTPS 端口，独立于本机负责人端口")
	stop := flag.Bool("stop", false, "结束此数据目录正在运行的服务")
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flag.NArg() != 0 {
		return errors.New("未知参数：" + flag.Arg(0))
	}
	explicitLAN, explicitLANPort := false, false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "lan" {
			explicitLAN = true
		}
		if f.Name == "lan-port" {
			explicitLANPort = true
		}
	})
	if explicitLANPort && (*lanPort < 1024 || *lanPort > 65436) {
		return errors.New("局域网端口需介于 1024 和 65436 之间")
	}
	if err := validateStartupFlags(*compact, *browser, *noBrowser, *noDesktop); err != nil {
		return err
	}
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
	startupLogRoot = dataDir
	if *stop {
		return stopExisting(dataDir)
	}
	background := desktopBackgroundOptions{foreground: *foreground, noBrowser: *noBrowser, noDesktop: *noDesktop, maintenance: *restore != "" || *relocate != "" || *verify != ""}
	if handled, err := startDesktopBackground(dataDir, background); handled || err != nil {
		return err
	}
	lock, err := platform.AcquireLock(filepath.Join(dataDir, "app.lock"))
	if err != nil {
		if *restore != "" || *relocate != "" {
			return errors.New("数据目录正在使用或无法加锁；请先退出服务再恢复")
		}
		info, e := existing(dataDir)
		if e != nil {
			return fmt.Errorf("无法锁定数据目录，且未发现可验证的运行实例: %w", err)
		}
		defer func() {
			if runErr == nil {
				desktopServiceReady(info.URL)
			}
		}()
		fmt.Println("ProjNote 已在运行：", info.URL)
		if explicitLAN {
			selectedPort := *lanPort
			if !explicitLANPort {
				selectedPort = existingLANPort(info.URL)
			}
			if e = requestExistingLAN(info.URL, *lan, selectedPort); e != nil {
				return e
			}
		}
		if *compact {
			return requestExistingCompact(info.URL)
		}
		startup := desktopStartup(*compact, *browser, *noBrowser, *noDesktop, "1")
		if startup.desktop && existingDesktopEnabled(info.URL) {
			if e = requestExistingEntry(info.URL); e != nil {
				fmt.Println("桌面入口未启动：", e)
				presentStartupFailure("桌面入口未启动：" + e.Error() + "\n服务仍在运行：" + info.URL)
			}
		}
		if startup.browser {
			return platform.OpenBrowser(info.URL)
		}
		return nil
	}
	defer lock.Close()
	if err = backup.CheckInterrupted(dataDir); err != nil {
		return err
	}
	if *restore != "" && *relocate != "" {
		return errors.New("恢复和迁移不能同时执行")
	}
	if *relocate != "" {
		return relocateOffline(dataDir, *relocate)
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
	st, err := openWithBackup(dataDir)
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
	manager := newBackupManager(dataDir, st)
	app := &server.Server{Backup: manager, Store: st, DataDir: dataDir, Host: host, Version: version, RuntimeToken: info.Instance, Shutdown: cancel}
	configureAutoStart(app, dataDir)
	lanRuntime := server.NewLANRuntime(dataDir, info.URL, app.LANHandler(), st.RevokeAllLANSessions)
	app.StartLAN, app.StopLAN, app.LANInfo = lanRuntime.Start, lanRuntime.Stop, lanRuntime.Info
	app.RenewLANCertificate = lanRuntime.RenewCertificate
	defer func() {
		if err := lanRuntime.Close(); err != nil {
			slog.Error("LAN shutdown failed", "error", err)
		}
	}()
	var compactMu sync.Mutex
	var compactEntry io.Closer
	ensureCompact := func(expand bool) error {
		compactMu.Lock()
		defer compactMu.Unlock()
		if compactEntry != nil {
			var revealErr error
			if expand {
				revealErr = platform.ShowCompact(compactEntry)
			} else {
				revealErr = platform.ShowCompactEntry(compactEntry)
			}
			if revealErr == nil {
				return nil
			}
			_ = compactEntry.Close()
			compactEntry = nil
		}
		entry, e := platform.StartCompact(ctx, info.URL+"/#/compact", info.URL)
		if e != nil {
			return e
		}
		compactEntry = entry
		if expand {
			if e = platform.ShowCompact(entry); e != nil {
				_ = entry.Close()
				compactEntry = nil
				return e
			}
		}
		state := platform.CompactEntryState(entry)
		slog.Info("compact entry started", "backend", state.Backend, "limitation", state.Limitation)
		if state.Limitation != "" {
			fmt.Println(state.Limitation)
		}
		return nil
	}
	app.OpenCompact = func() error { return ensureCompact(true) }
	app.OpenCompactEntry = func() error { return ensureCompact(false) }
	defer func() {
		compactMu.Lock()
		defer compactMu.Unlock()
		if compactEntry != nil {
			_ = compactEntry.Close()
		}
	}()
	if err = app.InitializeExports(); err != nil {
		return err
	}
	defer app.CloseExports()
	srv.Handler = app.Handler()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ensureDaily(manager)
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ensureDaily(manager)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		if err := lanRuntime.Close(); err != nil {
			slog.Error("LAN shutdown failed", "error", err)
		}
		shutdownCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
	}()
	fmt.Printf("ProjNote %s\n本机地址：%s\n数据目录：%s\n关闭浏览器后服务仍运行，可在设置中退出或使用 --stop。\n", version, info.URL, dataDir)
	settings, e := st.Settings()
	if e != nil {
		return e
	}
	lanStartup := selectLANStartup(*lan, *lanPort, explicitLAN, explicitLANPort, settings)
	if lanStartup.enabled {
		if result, lanErr := lanRuntime.Start(lanStartup.port); lanErr != nil {
			slog.Error("LAN unavailable", "error", lanErr)
			startupNotice = "局域网协同未启动：" + lanErr.Error()
			fmt.Println("局域网协同未启动：", lanErr)
			presentStartupFailure("局域网协同未启动：" + lanErr.Error() + "\n本机负责人服务仍在运行：" + info.URL)
		} else {
			if lanStartup.persist {
				if err := st.SetSettings(map[string]any{"lan_enabled": "1", "lan_port": strconv.Itoa(lanStartup.port)}); err != nil {
					_ = lanRuntime.Stop()
					slog.Error("LAN preference save failed", "error", err)
					startupNotice = "局域网协同设置未能保存，监听已关闭：" + err.Error()
					fmt.Println("局域网协同设置未能保存，监听已关闭：", err)
				}
			}
			if result["running"] == true && lanRuntime.Info()["running"] == true {
				fmt.Printf("局域网 HTTPS：%v\n证书 SHA-256：%v\n首次连接请在本机设置下载并信任证书，核对指纹后配对。\n", result["addresses"], result["certificate_sha256"])
			}
		}
	} else if lanStartup.persist {
		if err := st.SetSettings(map[string]any{"lan_enabled": "0", "lan_port": strconv.Itoa(lanStartup.port)}); err != nil {
			slog.Error("LAN preference save failed", "error", err)
		}
	}
	startup := desktopStartup(*compact, *browser, *noBrowser, *noDesktop, fmt.Sprint(settings["desktop_default"]))
	if startup.desktop {
		if e := ensureCompact(startup.expand); e != nil {
			slog.Warn("compact unavailable", "error", e)
			fmt.Println("桌面入口未启动：", e)
			presentStartupFailure("桌面入口未启动：" + e.Error() + "\n服务仍在运行：" + info.URL + "\n诊断日志：" + logDir)
		}
	}
	if startup.browser {
		if e := platform.OpenBrowser(info.URL); e != nil {
			slog.Warn("browser unavailable", "error", e)
			fmt.Println("请手动打开上面的本机地址。")
			presentStartupFailure("工作台浏览器未打开：" + e.Error() + "\n服务仍在运行，请手动打开：" + info.URL)
		}
	}

	desktopServiceReady(info.URL)
	err = srv.Serve(listener)
	cancel()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type desktopStartupOptions struct{ desktop, expand, browser bool }

func desktopStartup(compact, browser, noBrowser, noDesktop bool, preference string) desktopStartupOptions {
	if compact {
		return desktopStartupOptions{desktop: !noDesktop, expand: !noDesktop}
	}
	return desktopStartupOptions{desktop: !noBrowser && !noDesktop && preference != "0", browser: browser && !noBrowser}
}
func validateStartupFlags(compact, browser, noBrowser, noDesktop bool) error {
	if compact && noDesktop {
		return errors.New("--compact 与 --no-desktop 不能同时使用")
	}
	if browser && noBrowser {
		return errors.New("--browser 与 --no-browser 不能同时使用")
	}
	if browser && compact {
		return errors.New("--browser 与 --compact 不能同时使用，请选择浏览器工作台或原生便签")
	}
	return nil
}

func existingDesktopEnabled(base string) bool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(base + "/api/settings")
	if err != nil {
		return true
	}
	defer response.Body.Close()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope) != nil {
		return true
	}
	return envelope.Data["desktop_default"] != "0"
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
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		lock, e := platform.AcquireLock(filepath.Join(root, "app.lock"))
		if e == nil {
			lock.Close()
			fmt.Println("服务已退出。")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("服务仍在完成活动请求，请稍后重试")
}
func ensureDaily(manager *backup.Manager) {
	if err := manager.RunDaily(); err != nil {
		slog.Error("automatic backup failed", "error", err)
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
		st, err := openWithBackup(root)
		if err != nil {
			return fmt.Errorf("当前数据库无法打开；已保留原数据，请先复制数据目录到新目录再恢复: %w", err)
		}
		info, e := newBackupManager(root, st).MigrationBackup("restore-rollback")
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
	if err = restored.RevokeAllLANSessions(); err != nil {
		_ = restored.Close()
		return fmt.Errorf("恢复后撤销旧局域网会话失败，请保持停服并重试: %w", err)
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

func requestExistingCompact(base string) error { return requestExistingDesktop(base, "compact") }
func requestExistingEntry(base string) error   { return requestExistingDesktop(base, "entry") }
func requestExistingDesktop(base, action string) error {
	request, err := http.NewRequest("POST", base+"/api/desktop/"+action, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-ProjNote-Request", "1")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil
	}
	if response.StatusCode == http.StatusNotFound && action == "entry" {
		return errors.New("正在运行的是旧版本，请先退出旧程序再启动新版以显示便签入口")
	}

	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&result)
	if result.Error.Message != "" {
		return errors.New(result.Error.Message)
	}
	return fmt.Errorf("桌面入口启动失败：HTTP %d", response.StatusCode)
}
