package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// ---- fixtures ----

func newEventsFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.db")
	st, err := store.New(path)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Seed(ctx, "human"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	mustCreate(t, st, "human", store.CreateInput{Type: domain.TypeReq, ID: reqID, Title: "會員核心", ParentID: projectID, Owner: "yilong"})
	mustCreate(t, st, "xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: issueA, Title: "A", ParentID: reqID, Owner: "xiaoxia"})
	mustCreate(t, st, "yilong", store.CreateInput{Type: domain.TypeReq, ID: archReq, Title: "架構", ParentID: projectID, Owner: "yilong"})
	mustCreate(t, st, "kaimadi", store.CreateInput{Type: domain.TypeIssue, ID: archStore, Title: "store", ParentID: archReq, Owner: "kaimadi"})
	return st, path
}

func quietBroadcaster(st *store.Store, interval time.Duration) *Broadcaster {
	b := NewBroadcaster(st)
	b.interval = interval
	b.logf = func(string, ...any) {}
	return b
}

// ---- SSE reader ----

type frame struct{ kind, id, data string }

type sseStream struct {
	body io.ReadCloser
	ch   chan frame
}

// openSSE：帶 Last-Event-ID／?since= 連線（lastID 空＝沒帶）。
func openSSE(t *testing.T, base, query, lastID string) *sseStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/events"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	s := &sseStream{body: resp.Body, ch: make(chan frame, 64)}
	go func() {
		defer close(s.ch)
		sc := bufio.NewScanner(resp.Body)
		for {
			f, ok := scanFrame(sc)
			if !ok {
				return
			}
			s.ch <- f
		}
	}()
	return s
}

func (s *sseStream) close() { _ = s.body.Close() }

// next：等下一個 frame（含心跳註解）；逾時回 ok=false。
func (s *sseStream) next(t *testing.T, timeout time.Duration) (frame, bool) {
	t.Helper()
	select {
	case f, ok := <-s.ch:
		return f, ok
	case <-time.After(timeout):
		return frame{}, false
	}
}

// scanFrame：讀到下一個空行＝一個 frame（`: ping\n\n` → kind=ping）。
func scanFrame(sc *bufio.Scanner) (frame, bool) {
	var f frame
	saw := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if saw {
				return f, true
			}
			continue
		}
		saw = true
		switch {
		case strings.HasPrefix(line, ":"):
			f.kind = "ping"
		case strings.HasPrefix(line, "id:"):
			f.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			f.kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			f.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return f, saw
}

func decodeEvent(t *testing.T, f frame) sseEvent {
	t.Helper()
	var ev sseEvent
	if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
		t.Fatalf("unmarshal event %q: %v", f.data, err)
	}
	return ev
}

// ---- 單元 ----

func TestProjectMatchesPrefixTrap(t *testing.T) {
	cases := []struct {
		project, node string
		want          bool
	}{
		{"", "Y20260916/REQ-A", true},
		{"Y20260916", "Y20260916", true},
		{"Y20260916", "Y20260916/REQ-A", true},
		{"Y20260916", "Y20260917/REQ-A", false},
		{"Y2026091", "Y20260916/REQ-A", false}, // 前綴陷阱
		{"Y20260916/REQ-A", "Y20260916/REQ-AB", false},
	}
	for _, tc := range cases {
		if got := projectMatches(tc.project, tc.node); got != tc.want {
			t.Errorf("projectMatches(%q,%q) = %v, want %v", tc.project, tc.node, got, tc.want)
		}
	}
}

func TestParseSince(t *testing.T) {
	mk := func(header, query string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/events"+query, nil)
		if header != "" {
			r.Header.Set("Last-Event-ID", header)
		}
		return r
	}
	if n, ok := parseSince(mk("12", "?since=3")); !ok || n != 12 {
		t.Errorf("Last-Event-ID 應優先：%d ok=%v", n, ok)
	}
	if n, ok := parseSince(mk("", "?since=3")); !ok || n != 3 {
		t.Errorf("?since=：%d ok=%v", n, ok)
	}
	if _, ok := parseSince(mk("", "")); ok {
		t.Error("沒帶不該有 since")
	}
	if _, ok := parseSince(mk("abc", "?since=-1")); ok {
		t.Error("非法值應視為沒帶")
	}
}

// ---- 閒置不查 DB ----

func TestBroadcasterIdleNoQuery(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 5*time.Millisecond)
	defer b.Close()

	time.Sleep(60 * time.Millisecond)
	if n := b.tailQueries.Load(); n != 0 {
		t.Fatalf("沒有連線不該查 DB：queries=%d", n)
	}
}

func TestBroadcasterStopsWhenLastLeaves(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 5*time.Millisecond)
	defer b.Close()

	sub, _, err := b.subscribe("")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	waitFor(t, time.Second, func() bool { return b.tailQueries.Load() > 0 }, "tail 應開始查 DB")
	b.unsubscribe(sub)
	time.Sleep(30 * time.Millisecond) // 讓最後一筆在途查詢落地
	before := b.tailQueries.Load()
	time.Sleep(50 * time.Millisecond)
	if after := b.tailQueries.Load(); after != before {
		t.Fatalf("最後一條斷掉後仍查 DB：%d → %d", before, after)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

// ---- 端到端 ----

func TestSSEEndToEnd(t *testing.T) {
	st, path := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()

	stream := openSSE(t, srv.URL, "", "")
	defer stream.close()

	// 從「另一個 Store 實例」寫入，模擬別的 process（CLI／MCP）。
	st2, err := store.New(path)
	if err != nil {
		t.Fatalf("store.New(2): %v", err)
	}
	defer func() { _ = st2.Close() }()
	mustTransition(t, st2, "xiaoxia", issueA, domain.StatusInProgress, "", nil)

	f, ok := stream.next(t, time.Second) // 驗收：1 秒內收到
	if !ok || f.kind != "node" {
		t.Fatalf("frame = %+v ok=%v, want 一筆 node 事件", f, ok)
	}
	ev := decodeEvent(t, f)
	if ev.NodeID != issueA {
		t.Errorf("node_id = %q, want %q", ev.NodeID, issueA)
	}
	if ev.Action != "transition" || ev.Field != "status" || ev.From != "todo" || ev.To != "in_progress" {
		t.Errorf("事件欄位不對：%+v", ev)
	}
	if ev.NodeType != "issue" || ev.ParentID != reqID {
		t.Errorf("node_type/parent_id 不對：type=%q parent=%q", ev.NodeType, ev.ParentID)
	}
	if want := []string{projectID, reqID}; !reflect.DeepEqual(ev.Ancestors, want) {
		t.Errorf("ancestors = %v, want %v", ev.Ancestors, want)
	}
	if fmt.Sprint(ev.ID) != f.id {
		t.Errorf("SSE id %q != data.id %d", f.id, ev.ID)
	}
	if ev.Actor != "xiaoxia" || ev.TS == "" {
		t.Errorf("actor/ts 不對：%q %q", ev.Actor, ev.TS)
	}
}

func TestSSEResumeBackfill(t *testing.T) {
	st, path := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()
	st2, err := store.New(path)
	if err != nil {
		t.Fatalf("store.New(2): %v", err)
	}
	defer func() { _ = st2.Close() }()

	s1 := openSSE(t, srv.URL, "", "")
	defer s1.close()
	mustTransition(t, st2, "xiaoxia", issueA, domain.StatusInProgress, "", nil)
	f, ok := s1.next(t, time.Second) // 驗收：1 秒內收到
	if !ok || f.kind != "node" {
		t.Fatalf("第一筆 = %+v ok=%v", f, ok)
	}
	lastID := f.id
	s1.close()

	// 斷線期間寫 3 筆
	ctx := context.Background()
	for i, text := range []string{"c1", "c2", "c3"} {
		if err := st2.Comment(ctx, "xiaoxia", issueA, text); err != nil {
			t.Fatalf("Comment %d: %v", i, err)
		}
	}

	// 帶 Last-Event-ID 重連：應收到剛好那 3 筆、不重複
	s2 := openSSE(t, srv.URL, "", lastID)
	defer s2.close()
	base := parseID(t, lastID)
	var ids []int64
	for i := 0; i < 3; i++ {
		fr, ok := s2.next(t, 2*time.Second)
		if !ok || fr.kind != "node" {
			t.Fatalf("補送第 %d 筆 = %+v ok=%v", i, fr, ok)
		}
		ids = append(ids, decodeEvent(t, fr).ID)
	}
	for i, id := range ids {
		if id != base+int64(i)+1 {
			t.Errorf("補送 id[%d] = %d, want %d（不重複、不漏）", i, id, base+int64(i)+1)
		}
	}
}

func parseID(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("bad id %q: %v", s, err)
	}
	return n
}

func TestSSEResetOverCap(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()

	ctx := context.Background()
	for i := 0; i < backfillCap+5; i++ {
		if err := st.Comment(ctx, "human", issueA, "x"); err != nil {
			t.Fatalf("Comment %d: %v", i, err)
		}
	}
	stream := openSSE(t, srv.URL, "?since=0", "")
	defer stream.close()
	f, ok := stream.next(t, 3*time.Second)
	if !ok || f.kind != "reset" {
		t.Fatalf("第一 frame = %+v ok=%v, want reset", f, ok)
	}
}

func TestSSEProjectFilter(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()
	ctx := context.Background()

	stream := openSSE(t, srv.URL, "?project="+reqID, "")
	defer stream.close()
	if err := st.Comment(ctx, "human", issueA, "在子樹內"); err != nil {
		t.Fatal(err)
	}
	f, ok := stream.next(t, 2*time.Second)
	if !ok || f.kind != "node" {
		t.Fatalf("子樹內事件 = %+v ok=%v", f, ok)
	}
	if ev := decodeEvent(t, f); ev.NodeID != issueA {
		t.Fatalf("node_id = %q, want %q", ev.NodeID, issueA)
	}

	// 子樹外的事件不該收到
	if err := st.Comment(ctx, "human", archStore, "在子樹外"); err != nil {
		t.Fatal(err)
	}
	if fr, ok := stream.next(t, 250*time.Millisecond); ok {
		t.Fatalf("子樹外事件不該收到：%+v", fr)
	}
}

func TestSSEProjectPrefixTrap(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()

	// "Y2026091" 不是 "Y20260916/..." 的祖先，不該收到它的事件。
	stream := openSSE(t, srv.URL, "?project=Y2026091", "")
	defer stream.close()
	if err := st.Comment(context.Background(), "human", issueA, "前綴陷阱"); err != nil {
		t.Fatal(err)
	}
	if fr, ok := stream.next(t, 250*time.Millisecond); ok {
		t.Fatalf("前綴陷阱：不該收到 %+v", fr)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 10*time.Millisecond)
	b.heartbeat = 20 * time.Millisecond
	srv := httptest.NewServer(NewWithBroadcaster(st, b))
	defer srv.Close()
	defer b.Close()

	stream := openSSE(t, srv.URL, "", "")
	defer stream.close()
	f, ok := stream.next(t, 2*time.Second)
	if !ok || f.kind != "ping" {
		t.Fatalf("心跳 frame = %+v ok=%v", f, ok)
	}
}

// 慢客戶端只斷自己，不影響其他連線。
func TestSSESlowClientIsolated(t *testing.T) {
	st, _ := newEventsFixture(t)
	b := quietBroadcaster(st, 5*time.Millisecond)
	defer b.Close()

	fast, _, err := b.subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.subscribe(""); err != nil {
		t.Fatal(err)
	}
	got := make(chan sseEvent, 4096)
	drained := make(chan struct{})
	go func() {
		for {
			select {
			case <-drained:
				return
			case e := <-fast.ch:
				got <- e
			}
		}
	}()

	ctx := context.Background()
	for i := 0; i < subBufferSize+120; i++ {
		if err := st.Comment(ctx, "human", issueA, "x"); err != nil {
			t.Fatalf("Comment %d: %v", i, err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return b.subCount() == 1 }, "慢客戶端應被斷，只剩 fast")
	close(drained)
	if len(got) == 0 {
		t.Fatal("fast 應仍收到事件")
	}
}
