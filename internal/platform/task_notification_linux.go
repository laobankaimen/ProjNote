//go:build linux

package platform

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"time"
)

func TaskNotificationStatus() map[string]any {
	_, err := exec.LookPath("notify-send")
	supported := err == nil && os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
	description := "到时发送桌面系统通知；程序需保持后台运行，勿扰模式可能隐藏通知。"
	if !supported {
		description = "当前环境没有可用的桌面通知服务，请在桌面会话中启动程序并安装 notify-send。"
	}
	return map[string]any{"supported": supported, "description": description}
}
func NotifyTask(ctx context.Context, title, body string) error {
	if TaskNotificationStatus()["supported"] != true {
		return fmt.Errorf("当前环境无法发送桌面通知")
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, "notify-send", "--app-name=ProjNote", "--urgency=normal", "--expire-time=15000", "--", title, html.EscapeString(body))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("桌面通知发送失败，请检查通知服务: %w", err)
	}
	return nil
}
