package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"project_board/internal/store"
)

// ---------------------------------------------------------------------------
// harness：暫存 DB＋store＋Server＋JSON-RPC client 薄封裝
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &Server{st: st}
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// raw：發一則 JSON-RPC，回原始 response（notification 回 nil）。
func raw(t *testing.T, srv *Server, msg string) *rpcResp {
	t.Helper()
	out := srv.handleRaw(context.Background(), json.RawMessage(msg))
	if out == nil {
		return nil
	}
	var r rpcResp
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("response 非 JSON：%v\n%s", err, out)
	}
	return &r
}

// call：tools/call，回 (payload, isError)。協議層出錯直接 Fatal（那是 bug，不是案例）。
func call(t *testing.T, srv *Server, id int, name string, args map[string]any) (any, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": json.RawMessage(params),
	})
	r := raw(t, srv, string(msg))
	if r == nil || r.Error != nil {
		t.Fatalf("tools/call %s 掉到協議層錯誤：%+v", name, r)
	}
	var cr callResult
	if err := json.Unmarshal(r.Result, &cr); err != nil {
		t.Fatalf("CallToolResult 解不開：%v", err)
	}
	if len(cr.Content) == 0 {
		t.Fatalf("tools/call %s 回空 content", name)
	}
	var payload any
	if err := json.Unmarshal([]byte(cr.Content[0].Text), &payload); err != nil {
		// 錯誤訊息不是 JSON（純文字）→ payload 記原文
		payload = cr.Content[0].Text
	}
	return payload, cr.IsError
}

// mustCall：斷言成功，回 payload。
func mustCall(t *testing.T, srv *Server, id int, name string, args map[string]any) any {
	t.Helper()
	payload, isErr := call(t, srv, id, name, args)
	if isErr {
		t.Fatalf("tools/call %s 預期成功卻 isError：%v", name, payload)
	}
	return payload
}

// errCall：斷言 isError:true，且訊息含 wantSub。
func errCall(t *testing.T, srv *Server, id int, name string, args map[string]any, wantSub string) string {
	t.Helper()
	payload, isErr := call(t, srv, id, name, args)
	if !isErr {
		t.Fatalf("tools/call %s 預期 isError，卻成功：%v", name, payload)
	}
	msg, _ := payload.(string)
	if !strings.Contains(msg, wantSub) {
		t.Fatalf("tools/call %s 錯誤訊息 %q 不含 %q", name, msg, wantSub)
	}
	return msg
}

func nodeID(t *testing.T, payload any) string {
	t.Helper()
	m, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("payload 非節點：%v", payload)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("節點缺 id：%v", payload)
	}
	return id
}

// ---------------------------------------------------------------------------
// §11.8 自動化 round-trip：initialize→tools/list（20 個都在）→
// pb_create→pb_transition→pb_verify→pb_delete，全走 JSON-RPC。
// ---------------------------------------------------------------------------

func TestRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	_ = ctx

	// 1. initialize
	r := raw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	if r.Error != nil {
		t.Fatalf("initialize 失敗：%+v", r.Error)
	}
	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(r.Result, &initRes); err != nil {
		t.Fatalf("initialize 結果解不開：%v", err)
	}
	if initRes.ServerInfo.Name != "project_board" {
		t.Fatalf("serverInfo.name = %q", initRes.ServerInfo.Name)
	}

	// 2. tools/list：20 個 pb_* 都在（v0.1 的 14 個照 INTERFACE.md §2 順序，
	// v0.2 的 pb_deps 緊跟 pb_search；v0.3 的 pb_hook／pb_unhook／pb_hooks 收尾）。
	r = raw(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var listRes struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(r.Result, &listRes); err != nil {
		t.Fatalf("tools/list 解不開：%v", err)
	}
	wantTools := []string{
		"pb_tree", "pb_get", "pb_create", "pb_update", "pb_transition",
		"pb_assign", "pb_link", "pb_unlink", "pb_verify", "pb_comment",
		"pb_search", "pb_deps", "pb_history", "pb_stats", "pb_delete",
		"pb_hook", "pb_unhook", "pb_hooks", "pb_commit_attach", "pb_set_repo",
	}
	if len(listRes.Tools) != len(wantTools) {
		t.Fatalf("tools 數量 = %d，期望 %d", len(listRes.Tools), len(wantTools))
	}
	for i, w := range wantTools {
		if listRes.Tools[i].Name != w {
			t.Fatalf("tools[%d] = %q，期望 %q", i, listRes.Tools[i].Name, w)
		}
	}

	// 3. pb_create：project（顯式 id）→ req → issue（自動 id）。
	p := mustCall(t, srv, 10, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260920", "title": "round trip",
	})
	if nodeID(t, p) != "Y20260920" {
		t.Fatalf("project id 跑掉：%v", p)
	}
	mustCall(t, srv, 11, "pb_create", map[string]any{
		"actor": "human", "type": "req", "parent_id": "Y20260920",
		"id": "Y20260920/REQ-RT", "title": "rt req",
	})
	issue := mustCall(t, srv, 12, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920/REQ-RT",
		"title": "Auto Gen Key", "owner": "xiaoxia",
	})
	issueID := nodeID(t, issue)
	if issueID != "Y20260920/REQ-RT/ISSUE-AUTO-GEN-KEY" {
		t.Fatalf("自動 id = %q，不合 §11.5 生成規則", issueID)
	}

	// 4. pb_transition：todo→in_progress→review。
	mustCall(t, srv, 13, "pb_transition", map[string]any{
		"actor": "xiaoxia", "id": issueID, "to": "in_progress",
	})
	got := mustCall(t, srv, 14, "pb_transition", map[string]any{
		"actor": "xiaoxia", "id": issueID, "to": "review",
	})
	if got.(map[string]any)["status"] != "review" {
		t.Fatalf("transition 後 status = %v", got)
	}

	// 5. pb_verify（他人驗收，非 self）→ done。
	got = mustCall(t, srv, 15, "pb_verify", map[string]any{
		"actor": "claude", "id": issueID, "note": "round-trip ok，mcp 測試全綠",
	})
	if got.(map[string]any)["status"] != "done" {
		t.Fatalf("verify 後 status = %v", got)
	}

	// 6. 自我驗收標記：另開一單，owner 自己 verify → history 有 [self-verified]。
	selfIssue := mustCall(t, srv, 16, "pb_create", map[string]any{
		"actor": "kaimake", "type": "issue", "parent_id": "Y20260920/REQ-RT",
		"id": "Y20260920/REQ-RT/ISSUE-SELF", "title": "self", "owner": "kaimake",
	})
	selfID := nodeID(t, selfIssue)
	mustCall(t, srv, 17, "pb_transition", map[string]any{
		"actor": "kaimake", "id": selfID, "to": "in_progress",
	})
	mustCall(t, srv, 18, "pb_transition", map[string]any{
		"actor": "kaimake", "id": selfID, "to": "review",
	})
	mustCall(t, srv, 19, "pb_verify", map[string]any{
		"actor": "kaimake", "id": selfID, "note": "自己驗自己",
	})
	hist := mustCall(t, srv, 20, "pb_history", map[string]any{"id": selfID, "limit": 1})
	entries := hist.([]any)
	if len(entries) != 1 || !strings.Contains(entries[0].(map[string]any)["note"].(string), "[self-verified]") {
		t.Fatalf("自我驗收沒標 [self-verified]：%v", hist)
	}
	stats := mustCall(t, srv, 21, "pb_stats", map[string]any{})
	if int(stats.(map[string]any)["self_verified_count"].(float64)) != 1 {
		t.Fatalf("self_verified_count 應為 1：%v", stats)
	}

	// 7. pb_delete：report 可刪；刪完 get 應找嘸。
	mustCall(t, srv, 22, "pb_create", map[string]any{
		"actor": "xiaoxia", "type": "report", "parent_id": issueID,
		"id": issueID + "/REPORT-xiaoxia-20260920", "title": "tmp report",
	})
	mustCall(t, srv, 23, "pb_delete", map[string]any{"actor": "xiaoxia", "id": issueID + "/REPORT-xiaoxia-20260920"})
	errCall(t, srv, 24, "pb_get", map[string]any{"id": issueID + "/REPORT-xiaoxia-20260920"}, "not found")

	// 8. tree／search 讀得到這棵樹。
	tree := mustCall(t, srv, 25, "pb_tree", map[string]any{"project": "Y20260920"})
	if len(tree.([]any)) != 4 { // project＋req＋2 issues（report 已刪）
		t.Fatalf("tree 筆數 = %d，期望 4", len(tree.([]any)))
	}
}

// ---------------------------------------------------------------------------
// 錯誤情境（DoD 點名＋非法轉移／缺 actor／撞名／depends_on）：
// 全部要回 isError:true，不是靜默通過。
// ---------------------------------------------------------------------------

func TestErrorCases(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260920", "title": "err cases",
	})
	mustCall(t, srv, 2, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-E1", "title": "e1",
	})

	// 非法狀態轉移：todo→done（done 只能從 review 進）。
	errCall(t, srv, 10, "pb_transition", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1", "to": "done",
	}, "illegal transition")

	// 缺 actor。
	errCall(t, srv, 11, "pb_transition", map[string]any{
		"id": "Y20260920/ISSUE-E1", "to": "in_progress",
	}, "missing required param")

	// actor 不在名冊。
	errCall(t, srv, 12, "pb_transition", map[string]any{
		"actor": "bob", "id": "Y20260920/ISSUE-E1", "to": "in_progress",
	}, "invalid owner")

	// ID 撞名。
	errCall(t, srv, 13, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-E1", "title": "dup",
	}, "id already exists")

	// depends_on 目標不存在。
	errCall(t, srv, 14, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-E1",
		"kind": "depends_on", "target": "Y20260920/ISSUE-NOPE",
	}, "does not exist")

	// 轉 blocked 沒理由。
	errCall(t, srv, 15, "pb_transition", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1", "to": "blocked",
	}, "missing block reason")

	// 非 review 節點 verify。
	errCall(t, srv, 16, "pb_verify", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1", "note": "x",
	}, "verify requires status=review")

	// verify 缺證據。
	errCall(t, srv, 17, "pb_verify", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1",
	}, "missing required param")

	// 有子節點不准刪。
	errCall(t, srv, 18, "pb_delete", map[string]any{
		"actor": "human", "id": "Y20260920",
	}, "cannot delete")

	// 樂觀鎖：拿 updated_at 改一次成功，再拿舊值改→conflict。
	// 注意 store 存秒級時間（nowSec）：兩次寫入若落在同一秒會比不出差異，
	// 先睡 1.2s 保證跨過秒邊界（store 自家測試是直接改 DB，這裡走真實路徑）。
	got := mustCall(t, srv, 19, "pb_get", map[string]any{"id": "Y20260920/ISSUE-E1"})
	stale := got.(map[string]any)["updated_at"].(string)
	mustCall(t, srv, 20, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1",
		"body": "v2-then-sleep",
	})
	time.Sleep(1200 * time.Millisecond)
	mustCall(t, srv, 21, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1",
		"body": "v3-fresh",
	})
	errCall(t, srv, 22, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1",
		"body": "v3-stale", "expected_updated_at": stale,
	}, "conflict")

	// expected_updated_at 格式爛掉→isError（不靜默當沒鎖）。
	errCall(t, srv, 23, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-E1",
		"body": "x", "expected_updated_at": "昨天",
	}, "RFC3339")

	// 未知 kind、爛 priority：照樣 isError，不自己腦補。
	errCall(t, srv, 24, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-E1",
		"kind": "teleport", "target": "x",
	}, "invalid link kind")
	errCall(t, srv, 25, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-E2", "title": "e2", "priority": "urgent",
	}, "invalid priority")
}

// ---------------------------------------------------------------------------
// 協議層：ping／未知 method／壞 JSON／notification 不回／未知 tool。
// ---------------------------------------------------------------------------

func TestProtocol(t *testing.T) {
	srv := newTestServer(t)

	r := raw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if r.Error != nil {
		t.Fatalf("ping 失敗：%+v", r.Error)
	}
	r = raw(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/fly"}`)
	if r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("未知 method 應 -32601：%+v", r)
	}
	r = raw(t, srv, `{"jsonrpc":"2.0","id":3`)
	if r.Error == nil || r.Error.Code != -32700 {
		t.Fatalf("壞 JSON 應 -32700：%+v", r)
	}
	// notification（無 id）不回。
	if out := raw(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); out != nil {
		t.Fatalf("notification 應不回，卻回：%+v", out)
	}
	// jsonrpc 版本不對＋有 id→-32600；無 id→不回。
	r = raw(t, srv, `{"jsonrpc":"1.0","id":4,"method":"ping"}`)
	if r.Error == nil || r.Error.Code != -32600 {
		t.Fatalf("版本錯應 -32600：%+v", r)
	}
	if out := raw(t, srv, `{"jsonrpc":"1.0","method":"ping"}`); out != nil {
		t.Fatalf("壞 notification 應不回，卻回：%+v", out)
	}
	// 未知 tool→-32602；缺 tool 名→-32602。
	r = raw(t, srv, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"pb_fly","arguments":{}}}`)
	if r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("未知 tool 應 -32602：%+v", r)
	}
	r = raw(t, srv, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"arguments":{}}}`)
	if r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("缺 tool 名應 -32602：%+v", r)
	}
	// params 爛掉（array）→-32602。
	r = raw(t, srv, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":[]}`)
	if r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("爛 params 應 -32602：%+v", r)
	}
}

// ---------------------------------------------------------------------------
// 寫入系 tools 全覆蓋：update／assign／link／unlink／comment＋search／history。
// ---------------------------------------------------------------------------

func TestWriteTools(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260920", "title": "wt",
	})
	mustCall(t, srv, 2, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-W1", "title": "w1",
	})

	// update：title＋sort（數字字串也吃）＋tags。
	got := mustCall(t, srv, 10, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W1",
		"title": "w1-new", "sort": "3", "tags": "a,b",
	})
	m := got.(map[string]any)
	if m["title"] != "w1-new" || int(m["sort"].(float64)) != 3 || m["tags"] != "a,b" {
		t.Fatalf("update 沒寫進去：%v", got)
	}
	// sort 型別爛掉→isError。
	errCall(t, srv, 11, "pb_update", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W1", "sort": "high",
	}, "invalid param")

	// assign。
	got = mustCall(t, srv, 12, "pb_assign", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W1", "owner": "kaimadi",
	})
	if got.(map[string]any)["owner"] != "kaimadi" {
		t.Fatalf("assign 沒換 owner：%v", got)
	}
	errCall(t, srv, 13, "pb_assign", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W1", "owner": "ghost",
	}, "invalid owner")

	// link（commit，不驗 target）→ unlink → 再 unlink 同 id 找嘸。
	link := mustCall(t, srv, 14, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-W1",
		"kind": "commit", "target": "8094064", "note": "收單",
	})
	linkID := int64(link.(map[string]any)["id"].(float64))
	mustCall(t, srv, 15, "pb_unlink", map[string]any{"actor": "human", "link_id": linkID})
	errCall(t, srv, 16, "pb_unlink", map[string]any{"actor": "human", "link_id": linkID}, "not found")
	// link_id 缺席→isError；字串數字也吃。
	errCall(t, srv, 17, "pb_unlink", map[string]any{"actor": "human"}, "missing required param")
	mustCall(t, srv, 18, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-W1",
		"kind": "url", "target": "https://example.com",
	})

	// depends_on 合法目標：先建 E2 再掛。
	mustCall(t, srv, 19, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-W2", "title": "w2",
	})
	mustCall(t, srv, 20, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-W1",
		"kind": "depends_on", "target": "Y20260920/ISSUE-W2",
	})
	// 有 depends_on 就算 note 空也能轉 blocked（§11.3 擇一）。
	got = mustCall(t, srv, 21, "pb_transition", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W1", "to": "blocked",
	})
	if got.(map[string]any)["status"] != "blocked" {
		t.Fatalf("有 depends_on 應能轉 blocked：%v", got)
	}

	// comment → 回最新 history；空 text 拒收。
	got = mustCall(t, srv, 22, "pb_comment", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W2", "text": "hello",
	})
	if got.(map[string]any)["action"] != "comment" {
		t.Fatalf("comment 回的不是 history：%v", got)
	}
	errCall(t, srv, 23, "pb_comment", map[string]any{
		"actor": "human", "id": "Y20260920/ISSUE-W2",
	}, "missing required param")

	// search 命中；history 帶 limit。
	found := mustCall(t, srv, 24, "pb_search", map[string]any{"query": "w1-new"})
	if len(found.([]any)) == 0 {
		t.Fatalf("search 沒命中")
	}
	hist := mustCall(t, srv, 25, "pb_history", map[string]any{"id": "Y20260920/ISSUE-W1", "limit": 2})
	if len(hist.([]any)) != 2 {
		t.Fatalf("history limit=2 應回 2 筆，實 %d", len(hist.([]any)))
	}
	// limit 字串爛掉→isError。
	errCall(t, srv, 26, "pb_history", map[string]any{"id": "Y20260920/ISSUE-W1", "limit": "many"}, "invalid param")
}

// ---------------------------------------------------------------------------
// v0.2：pb_search 走 SearchAdvanced（query 可省略、四者至少一、只回精簡欄位）＋
// pb_deps（ListDependsOn）＋ pb_tree 的 tag 子字串過濾。
// ---------------------------------------------------------------------------

func TestSearchDepsV02(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260921", "title": "v02",
	})
	mustCall(t, srv, 2, "pb_create", map[string]any{
		"actor": "human", "type": "req", "parent_id": "Y20260921",
		"id": "Y20260921/REQ-S", "title": "search req",
	})
	mustCall(t, srv, 3, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260921/REQ-S",
		"id": "Y20260921/REQ-S/ISSUE-A", "title": "登入流程全文檢索測試",
		"owner": "xiaoxia", "tags": "v0.2,ui", "body": "FTS trigram 中文子字串",
	})
	mustCall(t, srv, 4, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260921/REQ-S",
		"id": "Y20260921/REQ-S/ISSUE-B", "title": "other", "owner": "kaimake", "tags": "v0.21",
	})

	asMaps := func(v any) []map[string]any {
		t.Helper()
		arr, ok := v.([]any)
		if !ok {
			t.Fatalf("預期陣列，卻是：%v", v)
		}
		out := make([]map[string]any, 0, len(arr))
		for _, e := range arr {
			m, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("陣列元素不是物件：%v", e)
			}
			out = append(out, m)
		}
		return out
	}
	hasID := func(ms []map[string]any, id string) bool {
		for _, m := range ms {
			if m["id"] == id {
				return true
			}
		}
		return false
	}

	// FTS 路徑（≥3 字）：中文子字串命中，且只回 5 個精簡欄位。
	got := mustCall(t, srv, 10, "pb_search", map[string]any{"query": "全文檢索"})
	ms := asMaps(got)
	if !hasID(ms, "Y20260921/REQ-S/ISSUE-A") {
		t.Fatalf("FTS 中文子字串沒命中：%v", got)
	}
	for _, m := range ms {
		if len(m) != 5 {
			t.Fatalf("search 應只回 5 欄，卻是：%v", m)
		}
		for _, k := range []string{"id", "type", "title", "status", "owner"} {
			if _, ok := m[k]; !ok {
				t.Fatalf("search 缺欄 %s：%v", k, m)
			}
		}
		if _, ok := m["body"]; ok {
			t.Fatalf("search 不該回 body：%v", m)
		}
	}

	// tag 整段相符：v0.2 命中 A 不命中 B（B 的 v0.21 不是一段）。
	got = mustCall(t, srv, 11, "pb_search", map[string]any{"tag": "v0.2"})
	ms = asMaps(got)
	if !hasID(ms, "Y20260921/REQ-S/ISSUE-A") || hasID(ms, "Y20260921/REQ-S/ISSUE-B") {
		t.Fatalf("tag 整段相符壞掉：%v", got)
	}
	// query 省略、只給 owner 也能查。
	got = mustCall(t, srv, 12, "pb_search", map[string]any{"owner": "kaimake"})
	if ms = asMaps(got); !hasID(ms, "Y20260921/REQ-S/ISSUE-B") {
		t.Fatalf("owner 全等沒命中：%v", got)
	}
	// 兩字中文走 LIKE 退回，一樣查得到。
	got = mustCall(t, srv, 13, "pb_search", map[string]any{"query": "登入"})
	if ms = asMaps(got); !hasID(ms, "Y20260921/REQ-S/ISSUE-A") {
		t.Fatalf("兩字 LIKE 退回沒命中：%v", got)
	}
	// 四個全空→isError，不靜默回全庫。
	errCall(t, srv, 14, "pb_search", map[string]any{}, "至少要有一個")
	errCall(t, srv, 15, "pb_search",
		map[string]any{"query": "  ", "project": "", "tag": "", "owner": ""}, "至少要有一個")
	// project 過濾：限本專案命中，限別人回空陣列（不是錯）。
	got = mustCall(t, srv, 16, "pb_search", map[string]any{"query": "全文檢索", "project": "Y20260921"})
	if ms = asMaps(got); !hasID(ms, "Y20260921/REQ-S/ISSUE-A") {
		t.Fatalf("search project 過濾壞掉：%v", got)
	}
	if ms = asMaps(mustCall(t, srv, 17, "pb_search",
		map[string]any{"query": "全文檢索", "project": "Y20260921/NOPE"})); len(ms) != 0 {
		t.Fatalf("search 限不存在子樹應回空：%v", ms)
	}

	// pb_tree 的 tag 是子字串（跟 CLI 一樣）：v0.2 同時命中 A 與 B（v0.21 含 v0.2）。
	got = mustCall(t, srv, 20, "pb_tree", map[string]any{"project": "Y20260921", "tag": "v0.2"})
	if ms = asMaps(got); !hasID(ms, "Y20260921/REQ-S/ISSUE-A") || !hasID(ms, "Y20260921/REQ-S/ISSUE-B") {
		t.Fatalf("tree tag 子字串壞掉：%v", got)
	}

	// pb_deps：掛兩條不同專案的 depends_on，驗全庫／分專案過濾。
	mustCall(t, srv, 30, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260921/REQ-S/ISSUE-A",
		"kind": "depends_on", "target": "Y20260921/REQ-S/ISSUE-B", "note": "等 B",
	})
	mustCall(t, srv, 31, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260922", "title": "px",
	})
	mustCall(t, srv, 32, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260922",
		"id": "Y20260922/ISSUE-1", "title": "px1",
	})
	mustCall(t, srv, 33, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260922/ISSUE-1",
		"kind": "depends_on", "target": "Y20260921/REQ-S/ISSUE-A",
	})
	// 順手掛一條非 depends_on，pb_deps 不該回它。
	mustCall(t, srv, 34, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260922/ISSUE-1",
		"kind": "file", "target": "a.md",
	})
	got = mustCall(t, srv, 35, "pb_deps", map[string]any{})
	if ms = asMaps(got); len(ms) != 2 {
		t.Fatalf("deps 全庫應 2 筆，實：%v", got)
	} else {
		if ms[0]["from_id"] != "Y20260921/REQ-S/ISSUE-A" || ms[0]["target"] != "Y20260921/REQ-S/ISSUE-B" {
			t.Fatalf("deps 第一筆不對：%v", ms[0])
		}
	}
	got = mustCall(t, srv, 36, "pb_deps", map[string]any{"project": "Y20260921"})
	if ms = asMaps(got); len(ms) != 1 || ms[0]["from_id"] != "Y20260921/REQ-S/ISSUE-A" {
		t.Fatalf("deps project=Y20260921 應只回 1 筆：%v", got)
	}
	got = mustCall(t, srv, 37, "pb_deps", map[string]any{"project": "Y20260922"})
	if ms = asMaps(got); len(ms) != 1 || ms[0]["from_id"] != "Y20260922/ISSUE-1" {
		t.Fatalf("deps project=Y20260922 應只回 1 筆：%v", got)
	}
}

// ---------------------------------------------------------------------------
// resources：list／read tree・stats・node／未知。
// ---------------------------------------------------------------------------

func TestResources(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260920", "title": "res",
	})

	r := raw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	var lr struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(r.Result, &lr); err != nil {
		t.Fatalf("resources/list 解不開：%v", err)
	}
	if len(lr.Resources) != 2 || lr.Resources[0].URI != "board://tree" || lr.Resources[1].URI != "board://stats" {
		t.Fatalf("resources 應為 tree＋stats：%+v", lr.Resources)
	}

	read := func(id int, uri string) *rpcResp {
		t.Helper()
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "resources/read",
			"params": map[string]any{"uri": uri},
		})
		return raw(t, srv, string(msg))
	}
	r = read(2, "board://tree?project=Y20260920")
	if r.Error != nil {
		t.Fatalf("read tree 失敗：%+v", r.Error)
	}
	r = read(3, "board://stats")
	if r.Error != nil {
		t.Fatalf("read stats 失敗：%+v", r.Error)
	}
	r = read(4, "board://node/Y20260920")
	if r.Error != nil {
		t.Fatalf("read node 失敗：%+v", r.Error)
	}
	r = read(5, "board://node/Y20260920-NOPE")
	if r.Error == nil {
		t.Fatalf("read 不存在的 node 應回錯")
	}
	r = read(6, "board://nope")
	if r.Error == nil {
		t.Fatalf("未知 resource 應回錯")
	}
	r = read(7, "board://node/")
	if r.Error == nil {
		t.Fatalf("空 node id 應回錯")
	}
	r = read(8, "https://example.com/x")
	if r.Error == nil {
		t.Fatalf("非 board:// 應回錯")
	}
	// 缺 uri→-32602。
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "resources/read", "params": map[string]any{},
	})
	if r := raw(t, srv, string(msg)); r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("缺 uri 應 -32602：%+v", r)
	}
}

// ---------------------------------------------------------------------------
// 傳輸層：stdio（真跑 serveStdio，MCP 標準 Content-Length framing，
// 含 notification 不回＋batch＋舊逐行 JSON 相容）與 HTTP。
// ---------------------------------------------------------------------------

func frameJSON(s string) string {
	return "Content-Length: " + strconv.Itoa(len(s)) + "\r\n\r\n" + s
}

// scanFrames 拆解 framed 輸出為 JSON 片段（驗證輸出端已走 framing）。
func scanFrames(t *testing.T, out string) []string {
	t.Helper()
	var frames []string
	for out != "" {
		headerEnd := strings.Index(out, "\r\n\r\n")
		if headerEnd < 0 || !strings.HasPrefix(out, "Content-Length: ") {
			t.Fatalf("輸出不是 Content-Length frame：%q", out)
		}
		n, err := strconv.Atoi(strings.TrimSpace(out[len("Content-Length: "):headerEnd]))
		if err != nil || n < 0 {
			t.Fatalf("frame 標頭壞：%q", out)
		}
		bodyStart := headerEnd + 4
		if len(out) < bodyStart+n {
			t.Fatalf("frame body 被截斷")
		}
		frames = append(frames, out[bodyStart:bodyStart+n])
		out = out[bodyStart+n:]
	}
	return frames
}

func TestStdioTransport(t *testing.T) {
	srv := newTestServer(t)
	// 輸入混兩種：標準 frame（ping／batch）＋舊逐行 JSON（notification／壞 JSON）；輸出全 frame。
	in := frameJSON(`{"jsonrpc":"2.0","id":1,"method":"ping"}`) +
		"\n" + // 空行跳過
		"\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + // 不回
		frameJSON(`[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","method":"ping"}]`) + // batch：只回 1 則
		`not json` + "\n" // parse error 照回
	var out bytes.Buffer
	if err := serveStdio(context.Background(), srv.st, strings.NewReader(in), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	frames := scanFrames(t, out.String())
	if len(frames) != 3 {
		t.Fatalf("應回 3 則 frame（ping＋batch1則＋parse error），實 %d：\n%s", len(frames), out.String())
	}
	// 第 1、3 則單筆 response；第 2 則 batch array（只包非 notification 那則）。
	for i, f := range frames {
		if i == 1 {
			var arr []json.RawMessage
			if err := json.Unmarshal([]byte(f), &arr); err != nil || len(arr) != 1 {
				t.Fatalf("batch 應回 1 則 array：%s", f)
			}
			continue
		}
		var r rpcResp
		if err := json.Unmarshal([]byte(f), &r); err != nil {
			t.Fatalf("stdio 回的不是 JSON：%v", err)
		}
	}
	// batch 全 notification→不回任何東西。
	var out2 bytes.Buffer
	if err := serveStdio(context.Background(), srv.st,
		strings.NewReader(`[{"jsonrpc":"2.0","method":"ping"}]`+"\n"), &out2); err != nil {
		t.Fatalf("serveStdio batch-notif: %v", err)
	}
	if out2.Len() != 0 {
		t.Fatalf("全 notification batch 應無輸出，卻有：%s", out2.String())
	}
	// batch 整包爛掉→parse error。
	var out3 bytes.Buffer
	if err := serveStdio(context.Background(), srv.st,
		strings.NewReader(frameJSON("[oops")), &out3); err != nil {
		t.Fatalf("serveStdio bad-batch: %v", err)
	}
	fs := scanFrames(t, out3.String())
	if len(fs) != 1 {
		t.Fatalf("爛 batch 應 1 frame：%s", out3.String())
	}
	var r rpcResp
	if err := json.Unmarshal([]byte(fs[0]), &r); err != nil || r.Error == nil || r.Error.Code != -32700 {
		t.Fatalf("爛 batch 應 -32700：%s", out3.String())
	}
}

func TestStdioFramingHandshake(t *testing.T) {
	// 完整走一次 harness 標準握手（全部 frame）：initialize → initialized → tools/list。
	srv := newTestServer(t)
	var out bytes.Buffer
	in := frameJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`) +
		frameJSON(`{"jsonrpc":"2.0","method":"notifications/initialized"}`) +
		frameJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if err := serveStdio(context.Background(), srv.st, strings.NewReader(in), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	frames := scanFrames(t, out.String())
	if len(frames) != 2 {
		t.Fatalf("應 2 則 frame（initialize＋tools/list，notification 不回）：%d\n%s", len(frames), out.String())
	}
	var init rpcResp
	if err := json.Unmarshal([]byte(frames[0]), &init); err != nil || init.Error != nil {
		t.Fatalf("initialize 應成功：%v", init)
	}
	if !bytes.Contains(init.Result, []byte(`"project_board"`)) {
		t.Fatalf("initialize 應報 serverInfo name=project_board：%s", init.Result)
	}
	// client 請求更新版（2025-03-26）時，server 要回報自己支援的版本
	// （supportedProtocol，2024-11-05），不能盲 echo 新版本。
	if !bytes.Contains(init.Result, []byte(`"protocolVersion":"2024-11-05"`)) {
		t.Fatalf("initialize 應報 server 支援版 2024-11-05：%s", init.Result)
	}
	var tl rpcResp
	if err := json.Unmarshal([]byte(frames[1]), &tl); err != nil || tl.Error != nil {
		t.Fatalf("tools/list 應成功：%v", tl)
	}
	var lt struct {
		Tools []any `json:"tools"`
	}
	if err := json.Unmarshal(tl.Result, &lt); err != nil || len(lt.Tools) == 0 {
		t.Fatalf("tools/list 應帶工具清單：%s", tl.Result)
	}
}

func TestStdioNewlineJSON(t *testing.T) {
	// 新版 @modelcontextprotocol/sdk 的 stdio 走「逐行 JSON」：輸入一行一則、
	// 輸出一行一則（用 \n 切，非 Content-Length frame）。第一筆是普通 JSON 行
	// → 全程鎖定逐行 JSON 輸出。
	srv := newTestServer(t)
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	if err := serveStdio(context.Background(), srv.st, strings.NewReader(in), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("應回 2 行（initialize＋tools/list，notification 不回）：%d\n%s", len(lines), out.String())
	}
	if strings.Contains(out.String(), "Content-Length:") {
		t.Fatalf("逐行 JSON 模式不該吐 frame：%s", out.String())
	}
	var init rpcResp
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil || init.Error != nil {
		t.Fatalf("initialize 應成功：%v", init)
	}
	if !bytes.Contains(init.Result, []byte(`"protocolVersion":"2024-11-05"`)) {
		t.Fatalf("應報 server 支援版 2024-11-05：%s", init.Result)
	}
	var tl rpcResp
	if err := json.Unmarshal([]byte(lines[1]), &tl); err != nil || tl.Error != nil {
		t.Fatalf("tools/list 應成功：%v", tl)
	}
}

func TestHTTPTransport(t *testing.T) {
	srv := newTestServer(t)
	h := NewHTTPHandler(srv.st)

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST 應 200，實 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var r rpcResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("HTTP 回的不是 JSON：%v", err)
	}

	// GET→405（SSE 未實作，明講）。
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 應 405，實 %d", rec.Code)
	}

	// 純 notification→202 無 body。
	rec = post(`{"jsonrpc":"2.0","method":"ping"}`)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification 應 202 空 body，實 %d %q", rec.Code, rec.Body.String())
	}

	// batch→200 array。
	rec = post(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"ping"}]`)
	var arr []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil || len(arr) != 1 {
		t.Fatalf("batch 應回 1 則 array：%s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 全工具缺參數表：20 個 tool 空 args {} 全部要 isError（不靜默、不 panic）。
// 順便鎖死「唯讀 tool 不需要 actor 也能跑」——空參數下唯讀 tool 倒在缺 id/過濾條件，不倒在缺 actor。
// ---------------------------------------------------------------------------

func TestAllToolsRejectEmptyArgs(t *testing.T) {
	srv := newTestServer(t)
	for i, name := range []string{
		"pb_tree", "pb_get", "pb_create", "pb_update", "pb_transition",
		"pb_assign", "pb_link", "pb_unlink", "pb_verify", "pb_comment",
		"pb_search", "pb_deps", "pb_history", "pb_stats", "pb_delete",
		"pb_hook", "pb_unhook", "pb_hooks", "pb_commit_attach", "pb_set_repo",
	} {
		payload, isErr := call(t, srv, 100+i, name, map[string]any{})
		if name == "pb_tree" || name == "pb_stats" || name == "pb_deps" || name == "pb_hooks" {
			// 全可選參數：空 args 合法（全庫），不斷言 isError。
			if isErr {
				t.Fatalf("%s 空參數應合法（全可選），卻 isError：%v", name, payload)
			}
			continue
		}
		if !isErr {
			t.Fatalf("%s 空參數應 isError，卻成功：%v", name, payload)
		}
	}
}

// ---------------------------------------------------------------------------
// v0.3：pb_hook／pb_unhook／pb_hooks 只呼叫 store，不 exec herdr。
// sentinel 由 store 回（unknown harness／invalid target／missing node／not found）。
// ---------------------------------------------------------------------------

func TestHookV03(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260923", "title": "hook",
	})
	mustCall(t, srv, 2, "pb_create", map[string]any{
		"actor": "human", "type": "req", "parent_id": "Y20260923",
		"id": "Y20260923/REQ-H", "title": "hook req",
	})
	issue := "Y20260923/REQ-H/ISSUE-H1"
	mustCall(t, srv, 3, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260923/REQ-H",
		"id": issue, "title": "hook issue",
	})

	hookArgs := func() map[string]any {
		return map[string]any{
			"actor": "xiaoxia", "node_id": issue,
			"harness": "herdr", "target": "yilong",
		}
	}
	got := mustCall(t, srv, 4, "pb_hook", hookArgs())
	m := got.(map[string]any)
	if m["node_id"] != issue || m["actor"] != "xiaoxia" || m["harness"] != "herdr" || m["target"] != "yilong" {
		t.Fatalf("hook 回的訂閱欄位不對：%v", got)
	}
	if _, err := time.Parse(time.RFC3339, m["created_at"].(string)); err != nil {
		t.Fatalf("hook created_at 不是 RFC3339：%v", got)
	}
	// 重複訂閱＝更新訂閱者，不報錯。
	mustCall(t, srv, 5, "pb_hook", map[string]any{
		"actor": "kaimake", "node_id": issue,
		"harness": "herdr", "target": "yilong",
	})

	asMaps := func(v any) []map[string]any {
		t.Helper()
		arr, ok := v.([]any)
		if !ok {
			t.Fatalf("預期陣列，卻是：%v", v)
		}
		out := make([]map[string]any, 0, len(arr))
		for _, e := range arr {
			mm, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("陣列元素不是物件：%v", e)
			}
			out = append(out, mm)
		}
		return out
	}
	if ms := asMaps(mustCall(t, srv, 6, "pb_hooks", map[string]any{})); len(ms) != 1 {
		t.Fatalf("hooks 全列應 1 筆，實 %d", len(ms))
	}
	if ms := asMaps(mustCall(t, srv, 7, "pb_hooks", map[string]any{"node_id": issue})); len(ms) != 1 {
		t.Fatalf("hooks node_id 過濾應 1 筆，實 %d", len(ms))
	}
	if ms := asMaps(mustCall(t, srv, 8, "pb_hooks", map[string]any{"node_id": "Y20260923/REQ-H"})); len(ms) != 0 {
		t.Fatalf("hooks 他節點過濾應 0 筆，實 %d", len(ms))
	}

	// sentinel：harness 非 herdr、target 格式錯、缺 node_id、節點不存在。
	bad := hookArgs()
	bad["harness"] = "slack"
	errCall(t, srv, 9, "pb_hook", bad, "unknown harness")
	bad = hookArgs()
	bad["target"] = "YiLong"
	errCall(t, srv, 10, "pb_hook", bad, "invalid target")
	bad = hookArgs()
	delete(bad, "node_id")
	errCall(t, srv, 11, "pb_hook", bad, "missing required param")
	bad = hookArgs()
	bad["node_id"] = "Y20260923/REQ-H/ISSUE-NOPE"
	errCall(t, srv, 12, "pb_hook", bad, "not found")

	// unhook：成功回 ok；再刪一次＝不存在，回錯。
	mustCall(t, srv, 13, "pb_unhook", hookArgs())
	errCall(t, srv, 14, "pb_unhook", hookArgs(), "not found")
	bad = hookArgs()
	bad["harness"] = "slack"
	errCall(t, srv, 15, "pb_unhook", bad, "unknown harness")
	if ms := asMaps(mustCall(t, srv, 16, "pb_hooks", map[string]any{})); len(ms) != 0 {
		t.Fatalf("unhook 後應 0 筆，實 %d", len(ms))
	}
}

// ---------------------------------------------------------------------------
// 覆蓋率補點：分支到不了 store 的純殼分支，集中在這裡打。
// ---------------------------------------------------------------------------

func TestCoverageFillers(t *testing.T) {
	srv := newTestServer(t)

	// tools/call 不帶 arguments 鍵（args==nil 分支）。
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "pb_stats"},
	})
	r := raw(t, srv, string(msg))
	if r.Error != nil {
		t.Fatalf("無 arguments 的 call 應走到 tool（全可選），卻協議錯：%+v", r.Error)
	}

	// resources/read params 爛掉→-32602。
	r = raw(t, srv, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":[]}`)
	if r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("爛 resource params 應 -32602：%+v", r)
	}

	// board://tree 指到不存在的 project→store 回 not found→資源錯。
	msg, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "resources/read",
		"params": map[string]any{"uri": "board://tree?project=NOPE"},
	})
	r = raw(t, srv, string(msg))
	if r.Error == nil || !strings.Contains(r.Error.Message, "not found") {
		t.Fatalf("tree 指不存在 project 應回 not found：%+v", r)
	}

	// mustJSON  fallback：Marshal 不了的值（func）走 Sprintf，不 panic。
	if s := mustJSON(func() {}); s == "" {
		t.Fatal("mustJSON fallback 回空")
	}

	// pb_tree depth 爛掉→isError。
	errCall(t, srv, 10, "pb_tree", map[string]any{"depth": "deep"}, "invalid param")

	// pb_unlink link_id 字串數字照吃；爛字串→isError。
	mustCall(t, srv, 11, "pb_create", map[string]any{
		"actor": "human", "type": "project", "id": "Y20260920", "title": "cov",
	})
	mustCall(t, srv, 12, "pb_create", map[string]any{
		"actor": "human", "type": "issue", "parent_id": "Y20260920",
		"id": "Y20260920/ISSUE-C1", "title": "c1",
	})
	link := mustCall(t, srv, 13, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920/ISSUE-C1",
		"kind": "file", "target": "a.md",
	})
	linkID := int64(link.(map[string]any)["id"].(float64))
	mustCall(t, srv, 14, "pb_unlink", map[string]any{
		"actor": "human", "link_id": strconv.FormatInt(linkID, 10),
	})
	errCall(t, srv, 15, "pb_unlink", map[string]any{"actor": "human", "link_id": "abc"}, "invalid param")
	errCall(t, srv, 16, "pb_unlink", map[string]any{"actor": "human", "link_id": []any{}}, "must be a number")

	// pb_get 打到有 link＋有 children 的節點（getJSON 兩條 loop）。
	mustCall(t, srv, 17, "pb_link", map[string]any{
		"actor": "human", "from_id": "Y20260920", "kind": "doc", "target": "SPEC.md",
	})
	got := mustCall(t, srv, 18, "pb_get", map[string]any{"id": "Y20260920"})
	m := got.(map[string]any)
	if len(m["links"].([]any)) != 1 || len(m["children"].([]any)) != 1 {
		t.Fatalf("get 應含 1 link＋1 child：%v", got)
	}

	// 不存在的節點 comment／history→not found。
	errCall(t, srv, 19, "pb_comment", map[string]any{
		"actor": "human", "id": "Y20260920/NOPE", "text": "hi",
	}, "not found")
	errCall(t, srv, 20, "pb_history", map[string]any{"id": "Y20260920/NOPE"}, "not found")
}

// ---------------------------------------------------------------------------
// transport 錯誤分支：壞 reader／壞 writer／預取消 ctx／HTTP 壞 body。
// ---------------------------------------------------------------------------

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type errWriter struct {
	buf       bytes.Buffer
	failWrite bool
	failFlush bool
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.failWrite {
		return 0, errTestIO
	}
	return w.buf.Write(p)
}

var errTestIO = errTestIOValue{}

type errTestIOValue struct{}

func (errTestIOValue) Error() string { return "test io error" }

func TestTransportErrors(t *testing.T) {
	srv := newTestServer(t)

	// 讀一半斷線→serveStdio 回錯，不 panic。
	if err := serveStdio(context.Background(), srv.st,
		errReader{err: errTestIO}, &bytes.Buffer{}); err == nil {
		t.Fatal("壞 reader 應回錯")
	}
	// 寫不出去→回錯。
	if err := serveStdio(context.Background(), srv.st,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"),
		&errWriter{failWrite: true}); err == nil {
		t.Fatal("壞 writer 應回錯")
	}
	// ctx 預取消→直接回 ctx.Err，不讀 stdin。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serveStdio(ctx, srv.st, strings.NewReader(""), &bytes.Buffer{}); err != context.Canceled {
		t.Fatalf("預取消 ctx 應回 Canceled，實 %v", err)
	}
	// HTTP body 讀失敗→400。
	h := NewHTTPHandler(srv.st)
	req := httptest.NewRequest(http.MethodPost, "/mcp", errReader{err: errTestIO})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("壞 body 應 400，實 %d", rec.Code)
	}
}

func TestArgHelpers(t *testing.T) {
	args := map[string]any{
		"s":   "  x  ",
		"n":   float64(7),
		"num": "42",
		"bad": "x",
		"lst": []any{},
		"nil": nil,
	}
	if v, err := reqStr(args, "s"); err != nil || v != "x" {
		t.Fatalf("reqStr trim：%q %v", v, err)
	}
	if _, err := reqStr(args, "missing"); err == nil {
		t.Fatal("缺席應錯")
	}
	if _, err := reqStr(args, "nil"); err == nil {
		t.Fatal("nil 應錯")
	}
	if _, err := reqStr(args, "lst"); err == nil {
		t.Fatal("非字串應錯")
	}
	if v, err := reqText(args, "s"); err != nil || v != "  x  " {
		t.Fatalf("reqText 不 trim：%q %v", v, err)
	}
	if _, err := reqText(args, "lst"); err == nil {
		t.Fatal("reqText 非字串應錯")
	}
	if v, ok := optStr(args, "n"); ok || v != "" {
		t.Fatal("optStr 非字串應回 false")
	}
	if i, err := optInt(args, "n", 0); err != nil || i != 7 {
		t.Fatalf("optInt float：%d %v", i, err)
	}
	if i, err := optInt(args, "num", 0); err != nil || i != 42 {
		t.Fatalf("optInt 字串：%d %v", i, err)
	}
	if i, err := optInt(args, "missing", 9); err != nil || i != 9 {
		t.Fatalf("optInt 預設：%d %v", i, err)
	}
	if _, err := optInt(args, "bad", 0); err == nil {
		t.Fatal("爛數字字串應錯")
	}
	if _, err := optInt(args, "lst", 0); err == nil {
		t.Fatal("非數字型別應錯")
	}
	if i, err := reqInt64(args, "n"); err != nil || i != 7 {
		_ = i
		t.Fatalf("reqInt64 float：%v", err)
	}
	if i, err := reqInt64(args, "num"); err != nil || i != 42 {
		t.Fatalf("reqInt64 字串：%d %v", i, err)
	}
	if _, err := reqInt64(args, "missing"); err == nil {
		t.Fatal("缺席應錯")
	}
	if _, err := reqInt64(args, "bad"); err == nil {
		t.Fatal("爛字串應錯")
	}
	if _, err := reqInt64(args, "lst"); err == nil {
		t.Fatal("非數字型別應錯")
	}
	if tm, err := optTime(args, "missing"); err != nil || tm != nil {
		t.Fatalf("缺席應回 nil：%v %v", tm, err)
	}
	if tm, err := optTime(map[string]any{"t": "2026-09-20T01:03:00+08:00"}, "t"); err != nil || tm == nil {
		t.Fatalf("合法 RFC3339：%v %v", tm, err)
	}
	if m := parseQuery(""); len(m) != 0 {
		t.Fatalf("空 query：%v", m)
	}
	if m := parseQuery("a=1&&b=&=x"); m["a"] != "1" || m["b"] != "" {
		t.Fatalf("query 解析：%v", m)
	}
}

// TestToolDescriptionsStateMachine：tools/list 必須把允許的邊和收單路徑寫進描述。
// 為什麼：呼叫端只讀 tools/list，描述漏掉就會先送非法 to=done。
func TestToolDescriptionsStateMachine(t *testing.T) {
	srv := newTestServer(t)
	r := raw(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(r.Result, &list); err != nil {
		t.Fatalf("tools/list：%v", err)
	}
	type toolDoc struct {
		Description string
		ToDesc      string
	}
	byName := map[string]toolDoc{}
	for _, tool := range list.Tools {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		toDesc := ""
		if to, ok := props["to"].(map[string]any); ok {
			toDesc, _ = to["description"].(string)
		}
		byName[tool.Name] = toolDoc{tool.Description, toDesc}
	}
	tr := byName["pb_transition"]
	for _, want := range []string{
		"pb_verify",
		"illegal transition",
		"no-op",
		"missing block reason",
		"reopen",
	} {
		if !strings.Contains(tr.Description, want) {
			t.Errorf("pb_transition 描述缺 %q", want)
		}
	}
	for _, edge := range []string{
		"todo→in_progress|blocked|hold|cancel",
		"in_progress→review|blocked|hold|cancel",
		"review→done|in_progress|blocked|cancel",
		"blocked→in_progress|hold|cancel",
		"hold→todo|in_progress|cancel",
		"done→in_progress",
		"cancel→todo",
		"pb_verify",
	} {
		if !strings.Contains(tr.ToDesc, edge) {
			t.Errorf("to 參數說明缺 %q", edge)
		}
	}
	vf := byName["pb_verify"]
	for _, want := range []string{"review", "note", "pb_transition", "[self-verified]"} {
		if !strings.Contains(vf.Description, want) {
			t.Errorf("pb_verify 描述缺 %q", want)
		}
	}
}

// TestCommitAttachTool：pb_commit_attach 從訊息抓單號→link commit；查無此單落 skipped；不改狀態。
func TestCommitAttachTool(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{"actor": "human", "type": "project", "id": "Y20260920", "title": "p"})
	mustCall(t, srv, 2, "pb_create", map[string]any{"actor": "human", "type": "req", "parent_id": "Y20260920", "id": "Y20260920/REQ-G", "title": "g"})
	issue := "Y20260920/REQ-G/ISSUE-ONE"
	mustCall(t, srv, 3, "pb_create", map[string]any{"actor": "human", "type": "issue", "parent_id": "Y20260920/REQ-G", "id": issue, "title": "one"})

	got := mustCall(t, srv, 4, "pb_commit_attach", map[string]any{
		"actor": "human", "sha": "abc123def456",
		"message": "feat: x (#" + issue + ") and #Y20260920/REQ-G/ISSUE-NOPE",
	})
	gm, _ := got.(map[string]any)
	linked, _ := gm["linked"].([]any)
	skipped, _ := gm["skipped"].([]any)
	if len(linked) != 1 || linked[0] != issue {
		t.Fatalf("linked = %v, want [%s]", linked, issue)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v, want 1（查無此單）", skipped)
	}

	// 單上要有 commit link，目標是 sha。
	nb, _ := json.Marshal(mustCall(t, srv, 5, "pb_get", map[string]any{"id": issue}))
	if !strings.Contains(string(nb), "commit") || !strings.Contains(string(nb), "abc123def456") {
		t.Fatalf("get 應含 commit link：%s", nb)
	}
}

// TestSetRepoTool：pb_set_repo 設定 project repo；非 project 回 isError。
func TestSetRepoTool(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{"actor": "human", "type": "project", "id": "Y20260920", "title": "p"})
	mustCall(t, srv, 2, "pb_create", map[string]any{"actor": "human", "type": "req", "parent_id": "Y20260920", "id": "Y20260920/REQ-G", "title": "g"})

	got := mustCall(t, srv, 3, "pb_set_repo", map[string]any{
		"actor": "human", "project_id": "Y20260920", "url": "https://github.com/x/y", "path": "/tmp/y",
	})
	gm, _ := got.(map[string]any)
	if gm["url"] != "https://github.com/x/y" {
		t.Fatalf("set_repo 回傳不符：%v", got)
	}

	errCall(t, srv, 4, "pb_set_repo", map[string]any{
		"actor": "human", "project_id": "Y20260920/REQ-G", "url": "u",
	}, "不是 project")
}
