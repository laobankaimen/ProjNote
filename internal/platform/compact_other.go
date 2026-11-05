//go:build !windows && !linux

package platform

import (
	"context"
	"fmt"
	"io"
)

func startCompact(context.Context, string, string) (io.Closer, error) {
	return nil, fmt.Errorf("当前平台尚无原生浮动入口")
}
