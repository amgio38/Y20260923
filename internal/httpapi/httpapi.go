// Package httpapi：唯讀 REST ＋ dashboard（開碼弟負責，PB05-B）。
//
// 契約凍結（dev_docs/API_CONTRACT.md §5，2026-09-20）：
//
//	func New(st *store.Store) http.Handler
//
// 回傳「已掛好 REST ＋ dashboard」的單一 http.Handler；`pb serve` 直接以
// `mux.Handle("/", httpapi.New(st))` 掛載，MCP(HTTP) 由 `/mcp` 另行掛載。
//
// 端點一律唯讀、直接呼叫 internal/store，不自行寫 SQL；
// 回應形狀權威：docs/INTERFACE.md §3。錯誤：store.ErrNotFound → 404、
// 其餘 → 500（由 web handler 統一轉，見 internal/web Source 契約）。
package httpapi

import (
	"net/http"

	"project_board/internal/store"
	"project_board/internal/web"
)

// New 依契約回傳掛好 REST ＋ dashboard 的 handler。
//
// 內部把 store 接到 web.Source（storeSource），再交給 web.NewHandler 統一
// 處理路由與 404／500 對映。
func New(st *store.Store) http.Handler {
	return web.NewHandler(newSource(st))
}
