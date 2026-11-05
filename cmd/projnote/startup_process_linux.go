//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const desktopChildPipeEnv = "PROJNOTE_INTERNAL_READY_FD"

var desktopReadyPipe *os.File

type desktopReadyMessage struct {
	URL    string `json:"url,omitempty"`
	Error  string `json:"error,omitempty"`
	Notice string `json:"notice,omitempty"`
}

func prepareDesktopProcess() error {
	marker := os.Getenv(desktopChildPipeEnv)
	if marker == "" {
		return nil
	}
	_ = os.Unsetenv(desktopChildPipeEnv)
	if marker != "3" {
		return errors.New("后台启动握手无效")
	}
	file := os.NewFile(3, "projnote-ready")
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		_ = file.Close()
		return errors.New("后台启动握手管道无效")
	}
	syscall.CloseOnExec(3) // GTK and later subprocesses must not retain the pipe.
	desktopReadyPipe = file
	return nil
}
func desktopServiceReady(base string) {
	if desktopReadyPipe == nil {
		return
	}
	_ = json.NewEncoder(desktopReadyPipe).Encode(desktopReadyMessage{URL: base, Notice: startupNotice})
	_ = desktopReadyPipe.Close()
	desktopReadyPipe = nil
}
func desktopProcessDone(err error) {
	if desktopReadyPipe == nil {
		return
	}
	message := desktopReadyMessage{}
	if err != nil {
		message.Error = err.Error()
	}
	_ = json.NewEncoder(desktopReadyPipe).Encode(message)
	_ = desktopReadyPipe.Close()
	desktopReadyPipe = nil
}
func presentStartupFailure(string) {}
func startDesktopBackground(root string, options desktopBackgroundOptions) (bool, error) {
	if desktopReadyPipe != nil || !shouldDetachDesktop("linux", os.Getenv("DISPLAY") != "", options) {
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	message, err := launchDetachedDesktop(exe, os.Args[1:], os.Environ(), root, 30*time.Second)
	if err != nil {
		return true, err
	}
	fmt.Printf("ProjNote 已在后台运行。\n本机地址：%s\n诊断日志：%s/logs\n关闭便签或终端不会退出服务；退出请在设置操作或使用 --stop。\n", message.URL, root)
	if message.Notice != "" {
		fmt.Println("启动提示：", message.Notice)
	}
	return true, nil
}
func launchDetachedDesktop(executable string, args, env []string, root string, timeout time.Duration) (desktopReadyMessage, error) {
	var message desktopReadyMessage
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return message, err
	}
	defer readPipe.Close()
	log, err := openStartupLog(root)
	if err != nil {
		_ = writePipe.Close()
		return message, err
	}
	cmd := exec.Command(executable, args...)
	for _, value := range env {
		if !strings.HasPrefix(value, desktopChildPipeEnv+"=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, desktopChildPipeEnv+"=3")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.ExtraFiles = []*os.File{writePipe}
	err = cmd.Start()
	_ = writePipe.Close()
	_ = log.Close()
	if err != nil {
		return message, fmt.Errorf("后台服务无法启动: %w", err)
	}
	type result struct {
		message desktopReadyMessage
		err     error
	}
	ready := make(chan result, 1)
	go func() {
		var value desktopReadyMessage
		err := json.NewDecoder(io.LimitReader(readPipe, 64<<10)).Decode(&value)
		ready <- result{value, err}
	}()
	select {
	case value := <-ready:
		if value.err == nil && value.message.Error == "" && value.message.URL != "" {
			_ = cmd.Process.Release()
			return value.message, nil
		}
		// Failure messages and EOF are not a successful detached startup.
		stopDetachedDesktop(cmd)
		if value.message.Error != "" {
			return message, errors.New(value.message.Error)
		}
		if value.err == nil {
			value.err = errors.New("后台子进程未返回本机地址")
		}
		return message, fmt.Errorf("后台服务未确认启动，请查看 %s/logs: %w", root, value.err)
	case <-time.After(timeout):
		stopDetachedDesktop(cmd)
		return message, fmt.Errorf("后台服务启动超时，已终止本次子进程；请查看 %s/logs", root)
	}
}
func stopDetachedDesktop(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
