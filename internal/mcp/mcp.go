// Package mcp：MCP（JSON-RPC 2.0）薄實作（開碼客負責）。
//
// 架構鐵律（docs/INTERFACE.md §0）：只呼叫 internal/store 的 Store 方法，
// 不寫 SQL、不自行判定狀態機。store 回什麼 sentinel error，就轉成對應的
// MCP isError:true＋清楚訊息，不靜默修正、不吞錯誤。
//
// 入口簽名凍結（小蝦 PB03 需要，dev_docs/API_CONTRACT.md §6 流程管理）：
//
//	func RunStdio(ctx context.Context, st *store.Store) error
//	func NewHTTPHandler(st *store.Store) http.Handler
package mcp

import (
	"context"
	"net/http"
	"os"

	"project_board/internal/store"
)

// serverVersion 是 MCP initialize 回報的版本（跟 binary 版號對齊由 cmd/pb 負責，這裡只報 mcp 殼版本）。
const serverVersion = "0.1.0"

// RunStdio 跑 JSON-RPC 2.0 over stdio（pb mcp），阻塞到 EOF／ctx 結束。
func RunStdio(ctx context.Context, st *store.Store) error {
	return serveStdio(ctx, st, os.Stdin, os.Stdout)
}

// NewHTTPHandler 回傳掛在 /mcp 的 Streamable HTTP handler（pb serve 用）。
// v0.1 只實作 POST（單一／batch JSON-RPC）；GET（SSE 串流）回 405，
// 各 harness 的手動相容清單（docs/INTEGRATION.md §7）仍保留當補充驗證。
func NewHTTPHandler(st *store.Store) http.Handler {
	return &httpHandler{srv: &Server{st: st}}
}
