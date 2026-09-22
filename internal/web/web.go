// Package web：單頁 dashboard（開碼弟負責，phase 1 起）。
//
// PB05-A「靜態 dashboard」階段以內嵌 fixture JSON 當資料來源，把樹／詳情／
// 統計／焦點面板整頁串起來。資料格式一律對齊 docs/INTERFACE.md §3 的 REST
// 回應；phase 2（PB05-B）只要把 Source 從 FixtureSource 換成打
// internal/httpapi 的實作，dashboard.html 完全不用重寫。
//
// 視覺規格為 docs/INTERFACE.md §4 的硬性要求（Google 後台風格：系統字體、
// 灰階、卡片、細邊框、扁平元件、低飽和狀態色點、無外部字型／框架）。
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed assets/*
var assetFS embed.FS

//go:embed fixtures/*.json
var fixtureFS embed.FS

// ErrNotFound 表示查詢的節點／事件不存在；handler 會轉成 HTTP 404。
var ErrNotFound = errors.New("not found")

// ErrBadRequest 表示查詢參數不合法（例：`/api/report?week=` 非 YYYY-MM-DD）；
// handler 會轉成 HTTP 400。
var ErrBadRequest = errors.New("bad request")

// Source 是 dashboard 的唯讀資料來源。每個方法回傳「已經是
// docs/INTERFACE.md §3 格式」的 JSON，handler 原樣輸出，不做二次轉換。
type Source interface {
	Health() (json.RawMessage, error)
	Tree(q url.Values) (json.RawMessage, error)
	Node(id string) (json.RawMessage, error)
	History(id string, limit int) (json.RawMessage, error)
	Stats(q url.Values) (json.RawMessage, error)
	Search(q url.Values) (json.RawMessage, error)
	Deps(q url.Values) (json.RawMessage, error)
	Checklist(q url.Values) (json.RawMessage, error)
	Report(q url.Values) (json.RawMessage, error)
	Meta() (json.RawMessage, error)
}

// Handler 提供 dashboard 單頁與唯讀 JSON 端點（標準庫 net/http，無框架）。
type Handler struct {
	src Source
	mux *http.ServeMux
}

// NewHandler 以給定的資料來源組出 dashboard handler。
func NewHandler(src Source) *Handler {
	h := &Handler{src: src}
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/assets/", h.handleAsset)
	mux.HandleFunc("/api/tree", h.handleTree)
	mux.HandleFunc("/api/node/", h.handleNode)
	mux.HandleFunc("/api/stats", h.handleStats)
	mux.HandleFunc("/api/search", h.handleSearch)
	mux.HandleFunc("/api/deps", h.handleDeps)
	mux.HandleFunc("/api/checklist", h.handleChecklist)
	mux.HandleFunc("/api/report", h.handleReport)
	mux.HandleFunc("/api/meta", h.handleMeta)
	h.mux = mux
	return h
}

// ServeHTTP 讓 Handler 直接滿足 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleIndex 服務 dashboard 單頁。SPA deep-link：任何非 API／非 healthz 路徑都回同一份單頁，
// 由前端依 URL（`/`、`/Y20260916`、`/Y20260916/REQ-…/ISSUE-…`…）還原畫面；
// `/api/` 下的未知路徑仍回 404（不吞成 HTML）。
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(dashboardHTML)
}

// handleAsset 服務內嵌的靜態資源（品牌 icon 等）。dashboard 不得依賴外部 URL，一律走這裡。
func (h *Handler) handleAsset(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if name == "" || strings.Contains(name, "..") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	body, err := assetFS.ReadFile("assets/" + name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(body)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Health()
	})
}

func (h *Handler) handleTree(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Tree(r.URL.Query())
	})
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Stats(r.URL.Query())
	})
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Search(r.URL.Query())
	})
}

// handleDeps 服務 v0.2 的依賴清單（`/api/deps?project=`）。
func (h *Handler) handleDeps(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Deps(r.URL.Query())
	})
}

// handleChecklist 服務 v0.3 母表清單（`/api/checklist?project=`）。
func (h *Handler) handleChecklist(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Checklist(r.URL.Query())
	})
}

// handleReport 服務 v0.3 週報（`/api/report?project=&week=`）。
func (h *Handler) handleReport(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Report(r.URL.Query())
	})
}

// handleMeta 服務 v0.5 型別／負責人清單（`/api/meta`）；dashboard 據此取代
// 頁內寫死的 OWNERS 與 type 清單。
func (h *Handler) handleMeta(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Meta()
	})
}

// handleNode 服務 /api/node/{id} 與 /api/node/{id}/history。// id 是路徑式（含 `/`），所以用單一 subtree pattern 手動切尾綴。
func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/node/")
	if rest == "" {
		writeError(w, http.StatusBadRequest, "missing node id")
		return
	}
	if strings.HasSuffix(rest, "/history") {
		id := strings.TrimSuffix(rest, "/history")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing node id")
			return
		}
		limit := queryInt(r.URL.Query(), "limit")
		h.writeSource(w, func() (json.RawMessage, error) {
			return h.src.History(id, limit)
		})
		return
	}
	h.writeSource(w, func() (json.RawMessage, error) {
		return h.src.Node(rest)
	})
}

// writeSource 統一處理資料來源錯誤：ErrNotFound→404、ErrBadRequest→400、其餘→500
// （不洩漏內部堆疊）。
func (h *Handler) writeSource(w http.ResponseWriter, get func() (json.RawMessage, error)) {
	body, err := get()
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, "not found")
		case errors.Is(err, ErrBadRequest):
			writeError(w, http.StatusBadRequest, "bad request")
		default:
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ---- helpers ----

func allowGet(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	writeJSON(w, status, body)
}

func queryInt(q url.Values, key string) int {
	v := q.Get(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
