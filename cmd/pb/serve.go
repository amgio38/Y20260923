package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"project_board/internal/domain"
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
	return buildServeHandlerWith(st, ghSecret, httpapi.NewBroadcaster(st))
}

// buildServeHandlerWith：同 buildServeHandler，但由呼叫者持有 SSE broadcaster，
// 讓 `pb serve` 關機時能先關掉所有 /api/events 連線（見 cmdServe）。
func buildServeHandlerWith(st *store.Store, ghSecret string, bc *httpapi.Broadcaster) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", httpapi.NewWithBroadcaster(st, bc))
	mux.Handle("/mcp", mcp.NewHTTPHandler(st))
	// GitHub webhook（v0.4 git 整合）：repo 事件 → 依 commit/PR 訊息中的單號掛 link。
	mux.HandleFunc("/api/integrations/github", integrations.GitHub(st, ghSecret))
	return mux
}

// newServeServer：`pb serve` 的 http.Server。
//
// SSE 連線（/api/events）不會自己結束：Shutdown 開始時先關掉 broadcaster，讓每條事件
// 串流回傳，Shutdown 才能完成——否則只要有一個 dashboard 開著，SIGTERM／Ctrl-C 後
// serve 就停不下來（Y20260920/REQ-DASHBOARD-LIVE-UPDATES 整合實測抓到）。
func newServeServer(st *store.Store, addr, ghSecret string) *http.Server {
	bc := httpapi.NewBroadcaster(st)
	srv := &http.Server{
		Addr:              addr,
		Handler:           buildServeHandlerWith(st, ghSecret, bc),
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.RegisterOnShutdown(bc.Close)
	return srv
}

// shutdownTimeout：serve 收到結束訊號後，等進行中的請求收尾的上限。
const shutdownTimeout = 5 * time.Second

// cmdServe：常駐 REST＋dashboard＋MCP(HTTP)。
// 啟動測試一律走 process 工具，禁裸 &／nohup。
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
	if err := a.requireExistingDB(*db); err != nil {
		return a.fail(err)
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
	srv := newServeServer(st, *addr, a.getenv("GH_WEBHOOK_SECRET"))
	// hook 輪詢：每 2 秒看新的 history（transition／verify），把訂了單的 harness 叫醒。
	// 喚醒失敗只記 log，不影響服務（裁示）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.hookLoop(ctx, st)
	// 收到 SIGTERM／Ctrl-C → 優雅關機（最多等 5 秒），ListenAndServe 隨即回 ErrServerClosed。
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	// 啟動訊息先出，讓 process 工具／維運看得到（正式跑時會被 log 收走）。
	a.logf("ProjectBoard 啟動：http://%s（REST＋dashboard＋MCP /mcp；hook 輪詢 %s；Ctrl-C 結束）\n", *addr, a.pollInterval)
	// owner 名冊來源一併印出：各 harness 的 cwd 常不在 project_board/，名冊不如預期時
	// 先看這裡是哪一層被讀到（Y20260920/ISSUE-OWNERS-TXT-CWD-PB）。
	a.logf("owners：%d 名（來源：%s）\n", len(domain.Owners), domain.OwnersSource())
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
	if err := a.requireExistingDB(*db); err != nil {
		return a.fail(err)
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
	// owner 名冊來源印到 stderr（MCP 協定走 stdout，stderr 是安全的 log 通道）：
	// 各 harness 的 `pb mcp` cwd 常不在 project_board/，名冊不如預期時先看這裡
	// 是哪一層被讀到（Y20260920/ISSUE-OWNERS-TXT-CWD-PB）。
	a.logf("owners：%d 名（來源：%s）\n", len(domain.Owners), domain.OwnersSource())
	if err := a.mcpRun(ctx, st); err != nil {
		return a.fail(err)
	}
	return exitOK
}
