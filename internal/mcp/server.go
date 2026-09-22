package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// Server 是無狀態的 JSON-RPC 分發器；唯一依賴是小蝦的 *store.Store。
type Server struct {
	st *store.Store
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 信封
// ---------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func errResp(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func okResp(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// handleRaw 處理一則訊息；回 nil 表示不回應（notification）。
func (s *Server) handleRaw(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var req rpcRequest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		out, _ := json.Marshal(errResp(nil, -32700, "parse error: "+err.Error()))
		return out
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		if len(req.ID) == 0 {
			return nil
		}
		out, _ := json.Marshal(errResp(req.ID, -32600, "invalid request"))
		return out
	}
	notification := len(req.ID) == 0
	resp := s.dispatch(ctx, &req)
	if notification || resp == nil {
		return nil
	}
	out, _ := json.Marshal(resp)
	return out
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return okResp(req.ID, s.handleInitialize(req.Params))
	case "ping":
		return okResp(req.ID, map[string]any{})
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": toolSchemas()})
	case "tools/call":
		return s.handleCall(ctx, req)
	case "resources/list":
		return okResp(req.ID, map[string]any{"resources": resourceList()})
	case "resources/read":
		return s.handleResourceRead(ctx, req)
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

// supportedProtocol 是 server 真正支援的 MCP protocol 版本。
// initialize 的回應必須報「server 支援的版本」，不能盲目 echo client 請求版
// （client 若帶更新版本，echo 回去會讓 SDK 判定協議不符而停擺）。
// 老 harness 請求 <= 支援版 → echo 相容；請求更新 → 回支援版，SDK 自行降版。
const supportedProtocol = "2024-11-05"

func (s *Server) handleInitialize(params json.RawMessage) map[string]any {
	version := supportedProtocol
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.ProtocolVersion != "" {
			if p.ProtocolVersion <= supportedProtocol {
				version = p.ProtocolVersion // 不更新的老 client：用它的版本
			}
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"serverInfo": map[string]any{"name": "project_board", "version": serverVersion},
	}
}

// ---------------------------------------------------------------------------
// tools/call：執行錯誤一律包成 isError:true（INTERFACE.md §2），
// 只有「呼叫協議本身」壞掉（缺 name、未知 tool）才走 JSON-RPC error。
// ---------------------------------------------------------------------------

func (s *Server) handleCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params: "+err.Error())
		}
	}
	if p.Name == "" {
		return errResp(req.ID, -32602, "invalid params: missing tool name")
	}
	t := findTool(p.Name)
	if t == nil {
		return errResp(req.ID, -32602, "unknown tool: "+p.Name)
	}
	args := p.Arguments
	if args == nil {
		args = map[string]any{}
	}
	payload, err := t.Handle(ctx, s.st, args)
	if err != nil {
		// store 的 sentinel／參數缺失全部轉 isError，不靜默修正、不吞錯誤。
		return okResp(req.ID, errorResult(err.Error()))
	}
	return okResp(req.ID, textResult(mustJSON(payload)))
}

func textResult(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

func errorResult(msg string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
		"isError": true,
	}
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// resources（唯讀）
// ---------------------------------------------------------------------------

func resourceList() []any {
	return []any{
		map[string]any{
			"uri": "board://tree", "name": "tree",
			"description": "節點樹（精簡陣列，可帶 ?project=&status=&owner=&type=）",
			"mimeType":    "application/json",
		},
		map[string]any{
			"uri": "board://stats", "name": "stats",
			"description": "統計（可帶 ?project=）",
			"mimeType":    "application/json",
		},
	}
}

func (s *Server) handleResourceRead(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p struct {
		URI string `json:"uri"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, -32602, "invalid params: "+err.Error())
		}
	}
	if p.URI == "" {
		return errResp(req.ID, -32602, "invalid params: missing uri")
	}
	text, err := s.readResource(ctx, p.URI)
	if err != nil {
		return errResp(req.ID, -32002, err.Error())
	}
	return okResp(req.ID, map[string]any{
		"contents": []any{map[string]any{
			"uri": p.URI, "mimeType": "application/json", "text": text,
		}},
	})
}

// readResource 解析 board://tree（可帶 query）、board://node/{id}、board://stats。
func (s *Server) readResource(ctx context.Context, uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "board://")
	if !ok {
		return "", fmt.Errorf("unknown resource: %s", uri)
	}
	path, query, _ := strings.Cut(rest, "?")
	q := parseQuery(query)
	switch {
	case path == "tree":
		nodes, err := s.st.Tree(ctx, store.TreeFilter{
			Project: q["project"], Status: q["status"], Owner: q["owner"],
			Type: domain.NodeType(q["type"]),
		})
		if err != nil {
			return "", err
		}
		out := make([]any, 0, len(nodes))
		for _, n := range nodes {
			out = append(out, briefJSON(n, 0))
		}
		return mustJSON(out), nil
	case path == "stats":
		st, err := s.st.Stats(ctx, q["project"])
		if err != nil {
			return "", err
		}
		return mustJSON(statsJSON(st)), nil
	case strings.HasPrefix(path, "node/"):
		id := strings.TrimPrefix(path, "node/")
		if id == "" {
			return "", fmt.Errorf("unknown resource: %s", uri)
		}
		node, links, children, err := s.st.Get(ctx, id)
		if err != nil {
			return "", err
		}
		return mustJSON(getJSON(node, links, children)), nil
	default:
		return "", fmt.Errorf("unknown resource: %s", uri)
	}
}

// parseQuery 解析簡易 a=b&c=d（resource URI 的 query；非法對敞開忽略，不擋讀取）。
func parseQuery(q string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(q, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// 共用 JSON 形狀（欄位命名照 INTERFACE.md §2／§3 範例）
// ---------------------------------------------------------------------------

var taipei = time.FixedZone("Asia/Taipei", 8*60*60)

func fmtT(t time.Time) string { return t.In(taipei).Format(time.RFC3339) }

func nodeJSON(n domain.Node) map[string]any {
	return map[string]any{
		"id": n.ID, "type": string(n.Type), "title": n.Title,
		"body": n.Body, "tags": n.Tags, "parent_id": n.ParentID,
		"status": string(n.Status), "owner": n.Owner,
		"priority": string(n.Priority), "sort": n.Sort,
		"created_at": fmtT(n.CreatedAt), "updated_at": fmtT(n.UpdatedAt),
	}
}

func briefJSON(n domain.Node, children int) map[string]any {
	return map[string]any{
		"id": n.ID, "type": string(n.Type), "title": n.Title,
		"status": string(n.Status), "owner": n.Owner,
		"children_count": children,
	}
}

func linkJSON(l domain.Link) map[string]any {
	return map[string]any{
		"id": l.ID, "from_id": l.FromID, "kind": string(l.Kind),
		"target": l.Target, "note": l.Note, "created_at": fmtT(l.CreatedAt),
	}
}

func historyJSON(h domain.HistoryEntry) map[string]any {
	return map[string]any{
		"id": h.ID, "node_id": h.NodeID, "ts": fmtT(h.TS),
		"actor": h.Actor, "action": string(h.Action),
		"field": h.Field, "from": h.FromVal, "to": h.ToVal, "note": h.Note,
	}
}

func getJSON(node domain.Node, links []domain.Link, children []domain.Node) map[string]any {
	out := nodeJSON(node)
	ls := make([]any, 0, len(links))
	for _, l := range links {
		ls = append(ls, linkJSON(l))
	}
	cs := make([]any, 0, len(children))
	for _, c := range children {
		cs = append(cs, briefJSON(c, 0))
	}
	out["links"] = ls
	out["children"] = cs
	return out
}

func statsJSON(st store.Stats) map[string]any {
	byStatus := map[string]any{}
	for s, c := range st.CountByStatus {
		byStatus[string(s)] = c
	}
	byOwner := map[string]any{}
	for o, c := range st.CountByOwner {
		byOwner[o] = c
	}
	prog := map[string]any{}
	for id, f := range st.ReqProgress {
		prog[id] = f
	}
	return map[string]any{
		"count_by_status": byStatus, "count_by_owner": byOwner,
		"req_progress": prog, "self_verified_count": st.SelfVerifiedCount,
	}
}
