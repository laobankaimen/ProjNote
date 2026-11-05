package platform

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
)

type CompactState struct {
	Backend    string
	Limitation string
}
type compactEntry struct {
	io.Closer
	state  CompactState
	closed atomic.Bool
}

var errCompactClosed = errors.New("原生便签已关闭，请重新创建")

func (e *compactEntry) State() CompactState { return e.state }
func (e *compactEntry) Close() error {
	if e == nil || e.Closer == nil {
		return nil
	}
	e.closed.Store(true)
	return e.Closer.Close()
}
func (e *compactEntry) Show() error {
	if e == nil || e.Closer == nil || e.closed.Load() {
		return errCompactClosed
	}
	if native, ok := e.Closer.(interface{ Show() error }); ok {
		return native.Show()
	}
	return errors.New("当前原生便签不支持重新显示")
}

// ShowCompact reveals an existing native panel without replacing its editor or
// reloading its draft. A closed entry returns an error so its owner can recreate it.
func ShowCompact(entry io.Closer) error {
	if entry == nil {
		return errCompactClosed
	}
	if native, ok := entry.(interface{ Show() error }); ok {
		return native.Show()
	}
	return errors.New("当前原生便签不支持重新显示")
}

// ShowEntry reveals the launcher without opening or replacing the note editor.
func (e *compactEntry) ShowEntry() error {
	if e == nil || e.Closer == nil || e.closed.Load() {
		return errCompactClosed
	}
	if native, ok := e.Closer.(interface{ ShowEntry() error }); ok {
		return native.ShowEntry()
	}
	return errors.New("当前原生便签不支持单独显示入口")
}
func ShowCompactEntry(entry io.Closer) error {
	if entry == nil {
		return errCompactClosed
	}
	if native, ok := entry.(interface{ ShowEntry() error }); ok {
		return native.ShowEntry()
	}
	return errors.New("当前原生便签不支持单独显示入口")
}

func CompactEntryState(entry io.Closer) CompactState {
	if e, ok := entry.(interface{ State() CompactState }); ok {
		return e.State()
	}
	return CompactState{}
}

// StartCompact creates a native edge tab and editable native note panel. No
// browser is opened by compact actions. Only an explicit full-workbench command
// may open a browser. Closing the entry never stops the HTTP service.
func StartCompact(ctx context.Context, compactURL, fullURL string) (io.Closer, error) {
	if ctx == nil {
		return nil, errors.New("原生便签需要有效的生命周期上下文")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	compact, err := localCompactURL(compactURL)
	if err != nil {
		return nil, err
	}
	full, err := localCompactURL(fullURL)
	if err != nil {
		return nil, err
	}
	if compact.Scheme != full.Scheme || compact.Host != full.Host {
		return nil, errors.New("原生便签和完整工作台必须使用同一本机服务")
	}
	return startCompact(ctx, compact.String(), full.String())
}
func localCompactURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil, errors.New("原生便签只接受本机HTTP地址")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("原生便签不能连接远程或局域网地址")
	}
	return u, nil
}

// Deprecated: compact is native and has a lifecycle owner. Call StartCompact;
// this compatibility function deliberately never launches a browser.
func OpenCompactWindow(value string) error {
	if _, err := localCompactURL(value); err != nil {
		return err
	}
	return errors.New("精简便签必须由StartCompact创建原生面板；不再使用浏览器应用模式")
}

type compactProcessCloser struct {
	once    sync.Once
	closed  atomic.Bool
	closeFn func() error
	err     error
}

func (c *compactProcessCloser) Close() error {
	c.once.Do(func() { c.closed.Store(true); c.err = c.closeFn() })
	return c.err
}
