package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/store"
)

// newApp 要把常駐服務接上（wire）。
func TestNewAppWires(t *testing.T) {
	a := newApp(io.Discard, io.Discard, func(string) string { return "" })
	if a.mcpRun == nil {
		t.Error("mcpRun 應由 wire 接上 mcp.RunStdio")
	}
	if a.listen == nil {
		t.Error("listen 應由 wire 接上")
	}
}

// run 是 main 的包裝（main 只多做 os.Exit）。
func TestRunEntrypoint(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut, func(string) string { return "" }); code != exitOK {
		t.Fatalf("run(help) = %d", code)
	}
	if !strings.Contains(out.String(), "ProjectBoard") {
		t.Error("run(help) 應印用法")
	}
}

// buildServeHandler：serve 的組裝＝REST＋dashboard（/、/api/*、/healthz）＋ MCP(HTTP)（/mcp）。
func TestBuildServeHandler(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = st.Close() }()
	h := buildServeHandler(st, "")
	for _, path := range []string{"/", "/healthz", "/api/tree", "/api/stats", "/mcp"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s 沒被路由（404）→ serve 組裝不完整", path)
		}
	}
	// GitHub webhook 端點有掛（POST，未帶簽章 secret 空 → 200）。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/github", strings.NewReader(`{"commits":[]}`))
	req.Header.Set("X-GitHub-Event", "push")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST /api/integrations/github = %d, want 200", rec.Code)
	}
}

func TestCmdServe(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")

	// 沒接線（listen 為 nil）
	if code := ta.run("serve"); code != exitUsage || !strings.Contains(ta.stderr(), "尚未接線") {
		t.Errorf("未接線 code=%d stderr=%q", code, ta.stderr())
	}

	// server 正常關閉 → 成功，且 addr 有傳進去
	var gotAddr string
	ta.a.listen = func(srv *http.Server) error {
		gotAddr = srv.Addr
		return http.ErrServerClosed
	}
	if code := ta.run("serve", "--addr", "127.0.0.1:0"); code != exitOK {
		t.Fatalf("正常關閉 code=%d stderr=%s", code, ta.stderr())
	}
	if gotAddr != "127.0.0.1:0" {
		t.Errorf("addr = %q, want 127.0.0.1:0", gotAddr)
	}
	if !strings.Contains(ta.stderr(), "啟動") {
		t.Errorf("應印啟動訊息，stderr=%q", ta.stderr())
	}

	// listen 回真錯誤 → 執行錯誤（1）
	ta.a.listen = func(*http.Server) error { return errors.New("boom") }
	if code := ta.run("serve"); code != exitErr {
		t.Errorf("真錯誤 code=%d, want %d", code, exitErr)
	}
	if !strings.Contains(ta.stderr(), "boom") {
		t.Errorf("stderr = %q", ta.stderr())
	}

	// PB_ADDR 當預設位址
	ta.vars["PB_ADDR"] = "127.0.0.1:9999"
	ta.a.listen = func(srv *http.Server) error {
		gotAddr = srv.Addr
		return nil
	}
	ta.mustRun(t, exitOK, "serve")
	if gotAddr != "127.0.0.1:9999" {
		t.Errorf("PB_ADDR 沒生效: %q", gotAddr)
	}

	// DB 開不起來 → 早退且不啟動 server
	if code := ta.run("serve", "--db", filepath.Join(t.TempDir(), "no-such-dir", "x.db")); code != exitErr {
		t.Errorf("壞 DB code=%d", code)
	}
}

func TestCmdMCP(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")

	// 沒接線
	if code := ta.run("mcp"); code != exitUsage || !strings.Contains(ta.stderr(), "尚未接線") {
		t.Errorf("未接線 code=%d stderr=%q", code, ta.stderr())
	}

	// 接上後：跑完即退（正常）
	var called bool
	ta.a.mcpRun = func(ctx context.Context, st *store.Store) error {
		called = true
		if ctx == nil {
			t.Error("ctx 不該是 nil")
		}
		return nil
	}
	if code := ta.run("mcp"); code != exitOK || !called {
		t.Fatalf("code=%d called=%v stderr=%s", code, called, ta.stderr())
	}

	// 回錯 → 執行錯誤
	ta.a.mcpRun = func(context.Context, *store.Store) error { return errors.New("stdio 壞了") }
	if code := ta.run("mcp"); code != exitErr || !strings.Contains(ta.stderr(), "stdio 壞了") {
		t.Errorf("code=%d stderr=%q", code, ta.stderr())
	}

	// 壞 flag／壞 DB
	if code := ta.run("mcp", "--nope"); code != exitUsage {
		t.Errorf("壞 flag code=%d", code)
	}
	if code := ta.run("mcp", "--db", filepath.Join(t.TempDir(), "no-such-dir", "x.db")); code != exitErr {
		t.Errorf("壞 DB code=%d", code)
	}
}
