// SSE 即時事件端點（Y20260920/REQ-DASHBOARD-LIVE-UPDATES/ISSUE-LIVE-B2）。
//
// 事件來源＝history 表（唯讀 `id > cursor`），不依賴 serve 自己 process 內的事件：
// CLI／各 agent 的 MCP 都是別的 process 直接寫 SQLite，只有讀 history 才收得到。
//
// 架構：
//   - 一個 Broadcaster 共用一個 tail 迴圈；有 ≥1 條 SSE 連線才啟動 ticker，
//     最後一條斷掉就停（閒置時完全不查 DB）。
//   - 每條連線有自己的緩衝 channel；寫不進去（慢客戶端）只斷那一條。
//   - 連線帶 Last-Event-ID／?since= 先從 DB 補送再進即時；>1000 筆改送 event: reset。
//   - 事件只帶 node_id／type／parent／ancestors／action／field／from→to／actor／ts（數百 bytes），
//     前端收到後只重抓該節點，不整頁重載。
//
// 事件合約見父 REQ（Y20260920/REQ-DASHBOARD-LIVE-UPDATES）「事件合約」節。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

const (
	// defaultEventInterval：tail 輪詢間隔（輕量；沒有連線時不跑）。
	defaultEventInterval = 500 * time.Millisecond
	// defaultEventHeartbeat：心跳註解行間隔（防代理／瀏覽器逾時）。
	defaultEventHeartbeat = 25 * time.Second
	// subBufferSize：每條連線的緩衝；滿了＝慢客戶端，只斷它。
	subBufferSize = 256
	// backfillCap：補送上限；超過改送 reset。
	backfillCap = 1000
	// tailBatch：一個 tick 最多讀幾筆新 history。
	tailBatch = 1000
	// noteLimit：事件 note 截斷長度（字元）。
	noteLimit = 200
)

// sseEvent：一筆推送事件（欄位對齊父 REQ 的事件合約；不直接 marshal domain 避免欄位名不同）。
type sseEvent struct {
	ID        int64    `json:"id"`
	NodeID    string   `json:"node_id"`
	NodeType  string   `json:"node_type"`
	ParentID  string   `json:"parent_id"`
	Ancestors []string `json:"ancestors"`
	Action    string   `json:"action"`
	Field     string   `json:"field"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Actor     string   `json:"actor"`
	Note      string   `json:"note"`
	TS        string   `json:"ts"`
}

// subscriber：一條 SSE 連線在 broadcaster 的訂閱。
type subscriber struct {
	ch     chan sseEvent
	done   chan struct{}
	filter string // ?project= 的過濾值（空＝全部）
}

// Broadcaster 是所有 SSE 連線共用的 tail＋fan-out。
type Broadcaster struct {
	st        *store.Store
	interval  time.Duration
	heartbeat time.Duration
	logf      func(format string, args ...any)

	baseCtx context.Context

	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	running bool
	cancel  context.CancelFunc
	cursor  int64

	tailQueries atomic.Int64 // 測試用：tail 實際查 DB 的次數（沒連線應為 0）
}

// NewBroadcaster：預設 500ms 輪詢、25s 心跳。
func NewBroadcaster(st *store.Store) *Broadcaster {
	return &Broadcaster{
		st:        st,
		interval:  defaultEventInterval,
		heartbeat: defaultEventHeartbeat,
		logf:      log.Printf,
		baseCtx:   context.Background(),
		subs:      map[*subscriber]struct{}{},
	}
}

// Close：停掉 tail 迴圈並結束所有訂閱（serve 收工／測試收尾用）。
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = false
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	for s := range b.subs {
		delete(b.subs, s)
		close(s.done)
	}
}

// ServeHTTP：GET /api/events（可選 ?project=<id>）。
func (b *Broadcaster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	project := r.URL.Query().Get("project")
	since, hasSince := parseSince(r)

	// 先訂閱再補送：兩者之間進來的事件會進緩衝，補送後用 id 去重，不漏不重。
	sub, seed, err := b.subscribe(project)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer b.unsubscribe(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// lastSent：已送出的最大 id；去重用，也決定即時流從哪接。
	lastSent := seed
	if hasSince {
		lastSent = since
	}
	if hasSince && since < seed {
		back, err := b.st.HistorySince(r.Context(), since, backfillCap+1)
		switch {
		case err != nil:
			// 補送查詢失敗不致命：從目前最新接即時流（不中斷連線）。
			b.logf("events: 補送查詢失敗（since=%d）：%v", since, err)
			lastSent = seed
		case len(back) > backfillCap:
			// 待補送太多：請前端整頁重載，之後照常收即時。
			if _, err := io.WriteString(w, "event: reset\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastSent = seed
		default:
			events, err := b.enrich(r.Context(), back)
			if err != nil {
				b.logf("events: 補送節點資訊失敗（since=%d）：%v", since, err)
				lastSent = seed
				break
			}
			for _, e := range events {
				if !projectMatches(project, e.NodeID) {
					continue
				}
				if err := writeSSEEvent(w, e); err != nil {
					return
				}
				flusher.Flush()
			}
			if n := len(back); n > 0 {
				lastSent = back[n-1].ID
			}
		}
	}

	beat := time.NewTicker(b.heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case e := <-sub.ch:
			if e.ID <= lastSent {
				continue // 補送已涵蓋 → 去重
			}
			if err := writeSSEEvent(w, e); err != nil {
				return
			}
			flusher.Flush()
			lastSent = e.ID
		case <-beat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// subscribe：註冊一條連線；第一條會啟動 tail 迴圈（cursor 從目前最新 id 起算）。
// 回傳的 seed 是「即時流會從哪個 id 之後開始送」的邊界。
func (b *Broadcaster) subscribe(filter string) (*subscriber, int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		maxID, err := b.st.MaxHistoryID(b.baseCtx)
		if err != nil {
			return nil, 0, err
		}
		b.cursor = maxID
		ctx, cancel := context.WithCancel(b.baseCtx)
		b.cancel = cancel
		b.running = true
		go b.tailLoop(ctx)
	}
	s := &subscriber{ch: make(chan sseEvent, subBufferSize), done: make(chan struct{}), filter: filter}
	b.subs[s] = struct{}{}
	return s, b.cursor, nil
}

// unsubscribe：移除訂閱；最後一條走掉就停 tail 迴圈（閒置不查 DB）。
func (b *Broadcaster) unsubscribe(s *subscriber) {
	b.mu.Lock()
	b.removeLocked(s)
	b.mu.Unlock()
}

// removeLocked：需持有 b.mu。
func (b *Broadcaster) removeLocked(s *subscriber) {
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	close(s.done)
	if len(b.subs) == 0 && b.running {
		b.running = false
		if b.cancel != nil {
			b.cancel()
			b.cancel = nil
		}
	}
}

// tailLoop：共用的追新迴圈（ctx 結束即停）。
func (b *Broadcaster) tailLoop(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	cursor := b.currentCursor()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.poll(ctx, &cursor); err != nil {
				// 查 DB 失敗：log、下個 tick 重試，不推進 cursor、不斷連線。
				b.logf("events: 輪詢 history 失敗：%v", err)
			}
		}
	}
}

// poll：讀一個 tick 的新 history、補節點資訊、fan-out、前進 cursor。
func (b *Broadcaster) poll(ctx context.Context, cursor *int64) error {
	b.tailQueries.Add(1)
	entries, err := b.st.HistorySince(ctx, *cursor, tailBatch)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	events, err := b.enrich(ctx, entries)
	if err != nil {
		return err
	}
	for _, e := range events {
		b.fanout(e)
	}
	*cursor = entries[len(entries)-1].ID
	b.mu.Lock()
	b.cursor = *cursor
	b.mu.Unlock()
	return nil
}

// fanout：把一筆事件送給所有訂閱（依 project 過濾）；緩衝滿＝慢客戶端，只斷它。
func (b *Broadcaster) fanout(e sseEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if !projectMatches(s.filter, e.NodeID) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.removeLocked(s)
		}
	}
}

func (b *Broadcaster) currentCursor() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cursor
}

// subCount：測試用。
func (b *Broadcaster) subCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// enrich：由 history 批次組出事件（node_type／parent_id／ancestors）。
// nodes 表同一個 tick 批次查，ancestors 需要的祖先一併多查一輪，不每筆一次。
func (b *Broadcaster) enrich(ctx context.Context, entries []domain.HistoryEntry) ([]sseEvent, error) {
	need := map[string]bool{}
	for _, e := range entries {
		if e.NodeID != "" {
			need[e.NodeID] = true
		}
	}
	byID, err := b.loadNodes(ctx, need)
	if err != nil {
		return nil, err
	}
	out := make([]sseEvent, 0, len(entries))
	for _, e := range entries {
		ev := sseEvent{
			ID: e.ID, NodeID: e.NodeID, Action: string(e.Action),
			Field: e.Field, From: e.FromVal, To: e.ToVal, Actor: e.Actor,
			Note: truncateRunes(e.Note, noteLimit), TS: formatEventTime(e.TS),
			Ancestors: []string{},
		}
		if n, ok := byID[e.NodeID]; ok {
			ev.NodeType = string(n.Type)
			ev.ParentID = n.ParentID
			ev.Ancestors = ancestorIDs(byID, n.ID)
		}
		out = append(out, ev)
	}
	return out, nil
}

// loadNodes：批次載入 ids 及其祖先（parents 一輪一輪補），回 id → Node。
func (b *Broadcaster) loadNodes(ctx context.Context, ids map[string]bool) (map[string]domain.Node, error) {
	byID := map[string]domain.Node{}
	loaded := map[string]bool{}
	pending := make([]string, 0, len(ids))
	for id := range ids {
		pending = append(pending, id)
	}
	// 每輪把 pending 的節點與其 parent 補進來；防環/不存在的 parent 會在下一輪自然收斂
	// （rounds 只是最後的防呆上界）。
	for rounds := 0; len(pending) > 0 && rounds < 10000; rounds++ {
		batch := pending
		pending = nil
		got, err := b.st.NodesByID(ctx, batch)
		if err != nil {
			return nil, err
		}
		for id := range got {
			byID[id] = got[id]
		}
		for _, id := range batch {
			loaded[id] = true
		}
		for _, id := range batch {
			n, ok := got[id]
			if !ok || n.ParentID == "" || loaded[n.ParentID] {
				continue
			}
			pending = append(pending, n.ParentID)
		}
	}
	return byID, nil
}

// ancestorIDs：由根到父的 id 陣列（不含自己）。找不到的祖先就在該處停。
func ancestorIDs(byID map[string]domain.Node, id string) []string {
	out := []string{}
	cur, ok := byID[id]
	if !ok {
		return out
	}
	for i := 0; i <= len(byID)+1; i++ {
		if cur.ParentID == "" {
			break
		}
		parent, ok := byID[cur.ParentID]
		if !ok {
			break
		}
		out = append(out, parent.ID)
		cur = parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// projectMatches：node_id 是否在 project 子樹（含 project 本身）；用 "/" 邊界比對，
// 避免 "Y2026091" 誤收 "Y20260916/..." 這類前綴陷阱。
func projectMatches(project, nodeID string) bool {
	if project == "" {
		return true
	}
	return nodeID == project || strings.HasPrefix(nodeID, project+"/")
}

// parseSince：Last-Event-ID 優先，其次 ?since=<id>；非法值視為沒帶。
func parseSince(r *http.Request) (int64, bool) {
	if v := strings.TrimSpace(r.Header.Get("Last-Event-ID")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n, true
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("since")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

// writeSSEEvent：一筆事件寫成 SSE frame（data 為單行 JSON）。
func writeSSEEvent(w io.Writer, e sseEvent) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: node\ndata: %s\n\n", e.ID, data)
	return err
}

// truncateRunes：截到 n 個字元（UTF-8 邊界安全）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// formatEventTime：事件時間用 store 的台北 RFC3339 格式（+08:00）。
func formatEventTime(t time.Time) string {
	return t.In(time.FixedZone("Asia/Taipei", 8*60*60)).Format(time.RFC3339)
}
