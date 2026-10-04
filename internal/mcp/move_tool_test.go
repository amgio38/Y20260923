package mcp

import "testing"

// TestMoveTool：MCP `pb_move`（搬單；Y20260920/REQ-MOVE-NODE）。
func TestMoveTool(t *testing.T) {
	srv := newTestServer(t)
	mustCall(t, srv, 1, "pb_create", map[string]any{"actor": "human", "type": "project", "id": "Y20260920", "title": "p"})
	mustCall(t, srv, 2, "pb_create", map[string]any{"actor": "human", "type": "project", "id": "Y20260921", "title": "q"})
	mustCall(t, srv, 3, "pb_create", map[string]any{
		"actor": "human", "type": "req", "parent_id": "Y20260920", "id": "Y20260920/REQ-X", "title": "x",
	})

	got := mustCall(t, srv, 4, "pb_move", map[string]any{
		"actor": "xiaoxia", "id": "Y20260920/REQ-X", "parent_id": "Y20260921", "note": "跨專案",
	})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("pb_move 回傳型別 = %T", got)
	}
	if m["id"] != "Y20260921/REQ-X" || m["old_id"] != "Y20260920/REQ-X" {
		t.Fatalf("pb_move = %v", m)
	}
	// 舊 id 由 pb_get 解析到新位置。
	g := mustCall(t, srv, 5, "pb_get", map[string]any{"id": "Y20260920/REQ-X"})
	if g.(map[string]any)["id"] != "Y20260921/REQ-X" {
		t.Fatalf("pb_get 舊 id = %v", g)
	}
}
