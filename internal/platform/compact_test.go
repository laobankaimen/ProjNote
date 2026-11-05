package platform

import (
	"context"
	"testing"
)

func TestCompactURLBoundary(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:18600/#/compact", "http://localhost:18600/", "http://[::1]:18600/#/compact"} {
		if _, err := localCompactURL(value); err != nil {
			t.Fatalf("loopback rejected: %s: %v", value, err)
		}
	}
	for _, value := range []string{"https://127.0.0.1:18600/", "http://example.com/", "http://192.168.1.10:18600/", "http://127.0.0.1.example.com/", "http://user@127.0.0.1/", "file:///tmp/local", "--app=http://127.0.0.1/"} {
		if _, err := localCompactURL(value); err == nil {
			t.Fatalf("unsafe compact URL accepted: %s", value)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StartCompact(ctx, "http://127.0.0.1:18600/#/compact", "http://127.0.0.1:18600/"); err != context.Canceled {
		t.Fatalf("canceled context should not launch a window: %v", err)
	}
	if _, err := StartCompact(context.Background(), "http://127.0.0.1:18600/#/compact", "http://127.0.0.1:18601/"); err == nil {
		t.Fatal("mismatched service origins accepted")
	}
}

type compactShowFake struct {
	body          string
	shows, closes int
	entryShows    int
	showErr       error
}

func (f *compactShowFake) Show() error      { f.shows++; return f.showErr }
func (f *compactShowFake) ShowEntry() error { f.entryShows++; return f.showErr }
func (f *compactShowFake) Close() error     { f.closes++; return nil }

func TestShowCompactReusesEditorAndRejectsClosedEntry(t *testing.T) {
	native := &compactShowFake{body: "尚未持久的正文"}
	entry := &compactEntry{Closer: native}
	for i := 0; i < 2; i++ {
		if err := ShowCompact(entry); err != nil {
			t.Fatal(err)
		}
	}
	if native.shows != 2 || native.closes != 0 || native.body != "尚未持久的正文" {
		t.Fatalf("show replaced or closed the editor: %#v", native)
	}
	if err := entry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ShowCompact(entry); err != errCompactClosed {
		t.Fatalf("closed entry accepted: %v", err)
	}
	if native.shows != 2 {
		t.Fatal("closed entry forwarded show")
	}
}
func TestShowCompactReportsUnavailableAndNativeErrors(t *testing.T) {
	if err := ShowCompact(nil); err != errCompactClosed {
		t.Fatalf("missing entry: %v", err)
	}
	var missing *compactEntry
	if err := ShowCompact(missing); err != errCompactClosed {
		t.Fatalf("nil entry: %v", err)
	}
	if err := ShowCompact(&compactProcessCloser{}); err == nil {
		t.Fatal("entry without Show accepted")
	}
	if err := ShowCompact(&compactEntry{Closer: &compactProcessCloser{}}); err == nil {
		t.Fatal("wrapped entry without Show accepted")
	}
	native := &compactShowFake{showErr: context.Canceled}
	if err := ShowCompact(&compactEntry{Closer: native}); err != context.Canceled {
		t.Fatalf("native error hidden: %v", err)
	}
}

func TestShowCompactEntryNeverOpensOrReplacesEditor(t *testing.T) {
	native := &compactShowFake{body: "未完成的正文"}
	entry := &compactEntry{Closer: native}
	for i := 0; i < 3; i++ {
		if err := ShowCompactEntry(entry); err != nil {
			t.Fatal(err)
		}
	}
	if native.entryShows != 3 || native.shows != 0 || native.closes != 0 || native.body != "未完成的正文" {
		t.Fatalf("entry reveal changed panel: %#v", native)
	}
	_ = entry.Close()
	if err := ShowCompactEntry(entry); err != errCompactClosed {
		t.Fatalf("closed entry accepted %v", err)
	}
	if err := ShowCompactEntry(nil); err != errCompactClosed {
		t.Fatal(err)
	}
	if err := ShowCompactEntry(&compactProcessCloser{}); err == nil {
		t.Fatal("unsupported reveal accepted")
	}
	native = &compactShowFake{showErr: context.Canceled}
	if err := ShowCompactEntry(&compactEntry{Closer: native}); err != context.Canceled {
		t.Fatal("native reveal error hidden", err)
	}
}
