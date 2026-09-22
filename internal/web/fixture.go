package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
)

// fixtureNode 是 tree.json 的節點形狀（巢狀）。欄位對齊 INTERFACE.md §3
// `/api/tree` 的巢狀樹；body／links 只在 /api/node/{id} 回。
type fixtureNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   string         `json:"status"`
	Owner    string         `json:"owner"`
	Priority string         `json:"priority"`
	Children []*fixtureNode `json:"children"`
}

type searchHit struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

// fixtureDep：`/api/deps` 的一筆依賴（由 nodes.json 的 links 推導，不改 fixture 檔）。
type fixtureDep struct {
	FromID string `json:"from_id"`
	Target string `json:"target"`
	Note   string `json:"note"`
}

// fixtureChecklist：`/api/checklist` 一列（v0.3 母表；欄位對齊 cmd/pb checklistJSON）。
type fixtureChecklist struct {
	Key     string `json:"key"`
	ItemID  string `json:"item_id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Owner   string `json:"owner"`
	IssueID string `json:"issue_id,omitempty"`
}

// FixtureSource 以內嵌 fixture JSON 實作 Source，供 PB05-A 獨立把整頁串起來。
// phase 2 由打 internal/httpapi 的 Source 取代，介面不變。
type FixtureSource struct {
	tree      json.RawMessage
	stats     json.RawMessage
	nodes     map[string]json.RawMessage
	history   map[string]json.RawMessage
	report    json.RawMessage
	checklist []fixtureChecklist
	meta      json.RawMessage
	deps      []fixtureDep
	index     []searchHit
}

// NewFixtureSource 載入內嵌 fixtures；fixture 壞掉屬程式錯誤，直接回 error。
func NewFixtureSource() (*FixtureSource, error) {
	return newFixtureSource(fixtureFS)
}

// newFixtureSource 以給定的檔案系統載入 fixtures，方便測試注入壞檔驗證錯誤路徑。
func newFixtureSource(fsys fs.FS) (*FixtureSource, error) {
	src := &FixtureSource{}
	var err error
	if src.tree, err = readFixture(fsys, "fixtures/tree.json"); err != nil {
		return nil, err
	}
	if src.stats, err = readFixture(fsys, "fixtures/stats.json"); err != nil {
		return nil, err
	}
	if src.nodes, err = readFixtureMap(fsys, "fixtures/nodes.json"); err != nil {
		return nil, err
	}
	if src.history, err = readFixtureMap(fsys, "fixtures/history.json"); err != nil {
		return nil, err
	}
	if src.report, err = readFixture(fsys, "fixtures/report.json"); err != nil {
		return nil, err
	}
	if src.checklist, err = readChecklistFixture(fsys, "fixtures/checklist.json"); err != nil {
		return nil, err
	}
	if src.meta, err = readFixture(fsys, "fixtures/meta.json"); err != nil {
		return nil, err
	}
	src.index, err = buildIndex(src.tree)
	if err != nil {
		return nil, err
	}
	src.deps = buildDeps(src.nodes)
	return src, nil
}

func (s *FixtureSource) Health() (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true,"schema_version":1}`), nil
}

func (s *FixtureSource) Tree(q url.Values) (json.RawMessage, error) {
	return s.tree, nil
}

func (s *FixtureSource) Node(id string) (json.RawMessage, error) {
	node, ok := s.nodes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return node, nil
}

func (s *FixtureSource) History(id string, limit int) (json.RawMessage, error) {
	raw, ok := s.history[id]
	if !ok {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		return raw, nil
	}
	var events []json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("fixture history %q: %w", id, err)
	}
	if len(events) > limit {
		events = events[:limit]
	}
	out, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *FixtureSource) Stats(q url.Values) (json.RawMessage, error) {
	return s.stats, nil
}

func (s *FixtureSource) Search(q url.Values) (json.RawMessage, error) {
	needle := strings.ToLower(strings.TrimSpace(q.Get("q")))
	project := q.Get("project")
	hits := make([]searchHit, 0)
	if needle == "" {
		return json.Marshal(hits)
	}
	for _, h := range s.index {
		if project != "" && !strings.HasPrefix(h.ID, project) {
			continue
		}
		if strings.Contains(strings.ToLower(h.ID), needle) ||
			strings.Contains(strings.ToLower(h.Title), needle) {
			hits = append(hits, h)
		}
	}
	return json.Marshal(hits)
}

// Deps：`/api/deps?project=`；由 nodes.json 的 depends_on links 推導，
// project 非空時只回 from_id 等於 project 或以前綴 project/ 開頭者。
func (s *FixtureSource) Deps(q url.Values) (json.RawMessage, error) {
	project := q.Get("project")
	out := make([]fixtureDep, 0, len(s.deps))
	for _, d := range s.deps {
		if project != "" && d.FromID != project && !strings.HasPrefix(d.FromID, project+"/") {
			continue
		}
		out = append(out, d)
	}
	return json.Marshal(out)
}

// Checklist：`/api/checklist?project=`；project 非空時只回 item_id 等於 project
// 或以前綴 project/ 開頭者（沒有 item 回 []，不是 null）。
func (s *FixtureSource) Checklist(q url.Values) (json.RawMessage, error) {
	project := q.Get("project")
	out := make([]fixtureChecklist, 0, len(s.checklist))
	for _, r := range s.checklist {
		if project != "" && r.ItemID != project && !strings.HasPrefix(r.ItemID, project+"/") {
			continue
		}
		out = append(out, r)
	}
	return json.Marshal(out)
}

// Report：`/api/report`；fixture 為固定一份（不隨 week／project 變動），
// 讓 dashboard 在離線 fixture 模式仍能畫出週報區塊。
func (s *FixtureSource) Report(q url.Values) (json.RawMessage, error) {
	return s.report, nil
}

// Meta：`/api/meta`；fixture 為固定一份（7 種 type＋owner 名冊），
// 讓 dashboard 在離線 fixture 模式仍能畫出型別／負責人選單。
func (s *FixtureSource) Meta() (json.RawMessage, error) {
	return s.meta, nil
}

// ---- fixture 載入 ----

func readFixture(fsys fs.FS, name string) (json.RawMessage, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", name, err)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("fixture %s is not valid json", name)
	}
	return json.RawMessage(b), nil
}

func readFixtureMap(fsys fs.FS, name string) (map[string]json.RawMessage, error) {
	raw, err := readFixture(fsys, name)
	if err != nil {
		return nil, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	return m, nil
}

// readChecklistFixture：母表 fixture 是陣列（不是 map），壞檔屬程式錯誤。
func readChecklistFixture(fsys fs.FS, name string) ([]fixtureChecklist, error) {
	raw, err := readFixture(fsys, name)
	if err != nil {
		return nil, err
	}
	var rows []fixtureChecklist
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	return rows, nil
}

func parseTree(raw json.RawMessage) ([]*fixtureNode, error) {
	var roots []*fixtureNode
	if err := json.Unmarshal(raw, &roots); err != nil {
		return nil, fmt.Errorf("fixture tree.json: %w", err)
	}
	return roots, nil
}

func buildIndex(raw json.RawMessage) ([]searchHit, error) {
	roots, err := parseTree(raw)
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	var walk func(nodes []*fixtureNode)
	walk = func(nodes []*fixtureNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			hits = append(hits, searchHit{
				ID:     n.ID,
				Type:   n.Type,
				Title:  n.Title,
				Status: n.Status,
				Owner:  n.Owner,
			})
			walk(n.Children)
		}
	}
	walk(roots)
	return hits, nil
}

// buildDeps：從 nodes.json 的 links 收集 kind=depends_on（穩定排序）。
// 節點 JSON 壞掉不致命——跳過該筆，維持 fixture 可載入。
func buildDeps(nodes map[string]json.RawMessage) []fixtureDep {
	var out []fixtureDep
	for id, raw := range nodes {
		var n struct {
			Links []struct {
				Kind   string `json:"kind"`
				Target string `json:"target"`
				Note   string `json:"note"`
			} `json:"links"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			continue
		}
		for _, l := range n.Links {
			if l.Kind == "depends_on" {
				out = append(out, fixtureDep{FromID: id, Target: l.Target, Note: l.Note})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromID != out[j].FromID {
			return out[i].FromID < out[j].FromID
		}
		return out[i].Target < out[j].Target
	})
	return out
}
