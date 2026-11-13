//go:build !windows && !linux

package platform

import (
	"context"
	"fmt"
)

func TaskNotificationStatus() map[string]any {
	return map[string]any{"supported": false, "description": "当前平台暂不支持桌面任务提醒。"}
}
func NotifyTask(context.Context, string, string) error {
	return fmt.Errorf("当前平台暂不支持桌面任务提醒")
}
