package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"project_board/internal/httpapi"
	"project_board/internal/integrations"
	"project_board/internal/mcp"
	"project_board/internal/store"
)

// defaultAddr：只綁本機（INTERFACE.md §4、OPERATIONS.md §2）；可用 --addr 或 env PB_ADDR 覆寫。
const defaultAddr = "127.0.0.1:8787"

// wire：把常駐服務的實作接上（單一接縫，測試可替換）。
func (a *app) wire() {
	a.mcpRun = mcp.RunStdio
	a.listen = func(srv *http.Server) error { return srv.ListenAndServe() }
	a.wireWaker()
}

// wireWaker：hook 喚醒的預設值（真 herdr ＋ 2 秒輪詢）。
// 測試不需要真的叫 herdr 時，把 a.waker 換成假實作即可（裁示）。
func (a *app) wireWaker() {
	if a.waker == nil {
		a.waker = herdrWaker{}
	}
	if a.pollInterval <= 0 {
		a.pollInterval = defaultHookPollInterval
	}
}

// buildServeHandler：`pb serve` 的 handler。
//
//	REST ＋ dashboard → 開碼弟的 internal/httpapi
//	MCP(HTTP)        → 開碼客的 internal/mcp（掛 /mcp）
func buildServeHandler(st *store.Store, ghSecret string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", httpapi.New(st))
	mux.Handle("/mcp", mcp.NewHTTPHandler(st))
	// GitHub webhook（v0.4 git 整合）：repo 事件 → 依 commit/PR 訊息中的單號掛 link。
	mux.HandleFunc("/api/integrations/github", integrations.GitHub(st, ghSecret))
	return mux
}

// cmdServe：常駐 REST＋dashboard＋MCP(HTTP)。
// 啟動測試一律走 process 工具，禁裸 &／nohup（rule.md §二、TOOLS.md）。
func (a *app) cmdServe(args []string) int {
	fs := a.newFlagSet("serve")
	db := a.dbFlag(fs)
	defAddr := a.getenv("PB_ADDR")
	if defAddr == "" {
		defAddr = defaultAddr
	}
	addr := fs.String("addr", defAddr, "listen 位址（預設 $PB_ADDR → "+defaultAddr+"）")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	st, err := openStore(*db)
	if err != nil {
		return a.fail(err)
	}
	defer func() { _ = st.Close() }()
	if a.listen == nil {
		return a.fail(errNotWired)
	}
	a.wireWaker()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           buildServeHandler(st, a.getenv("GH_WEBHOOK_SECRET")),
		ReadHeaderTimeout: 5 * time.Second,
	}
	// hook 輪詢：每 2 秒看新的 history（transition／verify），把訂了單的 harness 叫醒。
	// 喚醒失敗只記 log，不影響服務（裁示）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.hookLoop(ctx, st)
	// 啟動訊息先出，讓 process 工具／維運看得到（正式跑時會被 log 收走）。
	a.logf("ProjectBoard 啟動：http://%s（REST＋dashboard＋MCP /mcp；hook 輪詢 %s；Ctrl-C 結束）\n", *addr, a.pollInterval)
	if err := a.listen(srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return a.fail(err)
	}
	return exitOK
}

// cmdMCP：MCP over stdio（外部 harness 用）。
func (a *app) cmdMCP(args []string) int {
	fs := a.newFlagSet("mcp")
	db := a.dbFlag(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	st, err := openStore(*db)
	if err != nil {
		return a.fail(err)
	}
	defer func() { _ = st.Close() }()
	if a.mcpRun == nil {
		return a.fail(errNotWired)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.mcpRun(ctx, st); err != nil {
		return a.fail(err)
	}
	return exitOK
}
