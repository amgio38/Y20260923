package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"project_board/internal/domain"
)

// nodeCols 與 scanNode 的順序必須一致；parent_id 用 COALESCE 讓根節點（NULL）掃成空字串。
const nodeCols = "id, type, COALESCE(parent_id, ''), title, status, owner, priority, tags, body, sort, created_at, updated_at"

type rowScanner interface{ Scan(dest ...any) error }

// queryRower 讓同一份讀取邏輯同時吃 *sql.DB 與 *sql.Tx。
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanNode(sc rowScanner) (domain.Node, error) {
	var (
		n                       domain.Node
		ntype, status, priority string
		createdAt, updatedAt    string
	)
	if err := sc.Scan(&n.ID, &ntype, &n.ParentID, &n.Title, &status, &n.Owner, &priority,
		&n.Tags, &n.Body, &n.Sort, &createdAt, &updatedAt); err != nil {
		return domain.Node{}, err
	}
	n.Type = domain.NodeType(ntype)
	n.Status = domain.Status(status)
	n.Priority = domain.Priority(priority)
	var err error
	if n.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Node{}, err
	}
	if n.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Node{}, err
	}
	return n, nil
}

// getNodeQ 取單一節點；不存在回 ErrNotFound。
func getNodeQ(ctx context.Context, q queryRower, id string) (domain.Node, error) {
	n, err := scanNode(q.QueryRowContext(ctx, "SELECT "+nodeCols+" FROM nodes WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Node{}, fmt.Errorf("node %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Node{}, fmt.Errorf("get node %q: %w", id, err)
	}
	return n, nil
}

func (s *Store) getNode(ctx context.Context, id string) (domain.Node, error) {
	return getNodeQ(ctx, s.db, id)
}

func nodeExists(ctx context.Context, q queryRower, id string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM nodes WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("node exists %q: %w", id, err)
	}
	return true, nil
}

// parentType：讀父節點的型別（item 的 parent 必須是 req，這條由 store 驗）。
func parentType(ctx context.Context, q queryRower, id string) (domain.NodeType, error) {
	var t string
	if err := q.QueryRowContext(ctx, "SELECT type FROM nodes WHERE id = ?", id).Scan(&t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("parent %q: %w", id, ErrNotFound)
		}
		return "", fmt.Errorf("parent type %q: %w", id, err)
	}
	return domain.NodeType(t), nil
}

// historyEntry 一次事件的內容（ts 由 insertHistory 傳入）。
type historyEntry struct {
	nodeID string
	actor  string
	action domain.HistoryAction
	field  string
	from   string
	to     string
	note   string
}

// insertHistory 一律在呼叫端的 transaction 內執行（DATA_MODEL.md §10）。
func insertHistory(ctx context.Context, tx *sql.Tx, ts time.Time, e historyEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO history (node_id, ts, actor, action, field, from_val, to_val, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.nodeID, formatTime(ts), e.actor, string(e.action), e.field, e.from, e.to, e.note)
	if err != nil {
		return fmt.Errorf("insert history (%s): %w", e.action, err)
	}
	return nil
}

// fieldChange 供 Update 逐欄位寫 history。
type fieldChange struct{ field, from, to string }

func (s *Store) allNodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeCols+" FROM nodes ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	var out []domain.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return out, nil
}

// isSelfOrDescendant：id 是否為 ancestor 或其子孫（沿 parent_id 往上走，帶防環上界）。
func isSelfOrDescendant(byID map[string]domain.Node, id, ancestor string) bool {
	cur, ok := byID[id]
	if !ok {
		return false
	}
	for i := 0; i <= len(byID); i++ {
		if cur.ID == ancestor {
			return true
		}
		if cur.ParentID == "" {
			return false
		}
		next, ok := byID[cur.ParentID]
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// depthFrom：往上走到最頂祖先的步數（根＝0）；有防環上界。
func depthFrom(byID map[string]domain.Node, id string) int {
	cur, ok := byID[id]
	if !ok {
		return 0
	}
	depth := 0
	for i := 0; i <= len(byID); i++ {
		if cur.ParentID == "" {
			break
		}
		next, ok := byID[cur.ParentID]
		if !ok {
			break
		}
		depth++
		cur = next
	}
	return depth
}

// Tree 取節點清單（flat，帶 ParentID，供呼叫端自行組樹）。
//   - Project 非空：只取該節點子樹。
//   - Status／Owner／Tag／Type 非空：只留命中者，但**一併帶上其祖先**，否則樹會斷（dashboard 需要形狀）。
//   - Tag：tags 欄位（逗號分隔）含此子字串（API_CONTRACT.md §5-1）。
//   - Depth > 0：相對 project 根（無 Project 時相對各樹根）的深度上限。
//
// 排序：(深度, parent_id, sort, id)，結果穩定。
func (s *Store) Tree(ctx context.Context, f TreeFilter) ([]domain.Node, error) {
	all, err := s.allNodes(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.Node, len(all))
	for _, n := range all {
		byID[n.ID] = n
	}
	base := 0
	if f.Project != "" {
		if _, ok := byID[f.Project]; !ok {
			return nil, fmt.Errorf("project %q: %w", f.Project, ErrNotFound)
		}
		base = depthFrom(byID, f.Project)
	}
	relDepth := func(id string) int { return depthFrom(byID, id) - base }

	selected := make(map[string]bool)
	for _, n := range all {
		if f.Project != "" && !isSelfOrDescendant(byID, n.ID, f.Project) {
			continue
		}
		if f.Status != "" && string(n.Status) != f.Status {
			continue
		}
		if f.Owner != "" && n.Owner != f.Owner {
			continue
		}
		if f.Tag != "" && !strings.Contains(n.Tags, f.Tag) {
			continue
		}
		if f.Type != "" && n.Type != f.Type {
			continue
		}
		if f.Depth > 0 && relDepth(n.ID) > f.Depth {
			continue
		}
		selected[n.ID] = true
		// 帶上祖先（保持樹的形狀；祖先不受 Status／Owner／Type 過濾影響）
		cur := n
		for i := 0; i <= len(byID) && cur.ParentID != ""; i++ {
			parent, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			cur = parent
			selected[cur.ID] = true
			if cur.ID == f.Project {
				break
			}
		}
	}

	out := make([]domain.Node, 0, len(selected))
	for id := range selected {
		out = append(out, byID[id])
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := relDepth(out[i].ID), relDepth(out[j].ID)
		switch {
		case di != dj:
			return di < dj
		case out[i].ParentID != out[j].ParentID:
			return out[i].ParentID < out[j].ParentID
		case out[i].Sort != out[j].Sort:
			return out[i].Sort < out[j].Sort
		default:
			return out[i].ID < out[j].ID
		}
	})
	return out, nil
}

// Get 取單節點 ＋ 出向 links ＋ 子節點。
func (s *Store) Get(ctx context.Context, id string) (domain.Node, []domain.Link, []domain.Node, error) {
	n, err := s.getNode(ctx, id)
	if err != nil {
		return domain.Node{}, nil, nil, err
	}
	links, err := s.linksFrom(ctx, id)
	if err != nil {
		return domain.Node{}, nil, nil, err
	}
	children, err := s.children(ctx, id)
	if err != nil {
		return domain.Node{}, nil, nil, err
	}
	return n, links, children, nil
}

func (s *Store) children(ctx context.Context, id string) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+nodeCols+" FROM nodes WHERE parent_id = ? ORDER BY sort, id", id)
	if err != nil {
		return nil, fmt.Errorf("children of %q: %w", id, err)
	}
	defer rows.Close()
	var out []domain.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("children of %q: %w", id, err)
	}
	return out, nil
}

// ListNodeTypes 回 node_types 表全部的 type 定義，依 sort、key 排序（唯讀）。
// 供 /api/meta 與 Create 使用；新增 type ＝ INSERT 一筆 node_types，不必改 Go 或 schema。
func (s *Store) ListNodeTypes(ctx context.Context) ([]domain.TypeDef, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT key, id_prefix, id_shape, label, parent_type, sort FROM node_types ORDER BY sort, key")
	if err != nil {
		return nil, fmt.Errorf("read node_types: %w", err)
	}
	defer rows.Close()
	var defs []domain.TypeDef
	for rows.Next() {
		var (
			d                 domain.TypeDef
			key, shape, label string
			parent            string
		)
		if err := rows.Scan(&key, &d.IDPrefix, &shape, &label, &parent, &d.Sort); err != nil {
			return nil, fmt.Errorf("scan node_types: %w", err)
		}
		d.Key = domain.NodeType(key)
		d.IDShape = domain.IDShape(shape)
		d.Label = label
		d.ParentType = domain.NodeType(parent)
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read node_types: %w", err)
	}
	return defs, nil
}

// typeRegistry 由 ListNodeTypes 建 TypeRegistry，供 Create 做資料驅動的 ID 驗證／生成
// 與 parent 型別檢查。
func (s *Store) typeRegistry(ctx context.Context) (*domain.TypeRegistry, error) {
	// node_types 由 migration 0006 建立；比 0006 舊的 DB（migration 進行中）尚無此表，
	// 退回出廠定義，讓升級期間的寫入仍可用既有 5 種 type。
	var tables int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'node_types'").Scan(&tables); err != nil {
		return nil, fmt.Errorf("check node_types table: %w", err)
	}
	if tables == 0 {
		return domain.BuiltinRegistry(), nil
	}
	defs, err := s.ListNodeTypes(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := domain.NewTypeRegistry(defs)
	if err != nil {
		return nil, fmt.Errorf("node_types: %w", err)
	}
	return reg, nil
}

// Create 建節點：ID 空字串時用 domain.GenerateID；撞名回 ErrIDExists（不自動加尾碼）。
func (s *Store) Create(ctx context.Context, actor string, in CreateInput) (domain.Node, error) {
	if err := validateActor(actor); err != nil {
		return domain.Node{}, err
	}
	if in.Type == "" {
		return domain.Node{}, errors.New("create: type is required")
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.Node{}, errors.New("create: title is required")
	}
	now := nowSec()
	owner := in.Owner
	if owner == "" {
		owner = "unassigned"
	}
	if !domain.IsValidOwner(owner) {
		return domain.Node{}, fmt.Errorf("owner %q: %w", owner, domain.ErrInvalidOwner)
	}
	priority := in.Priority
	if priority == "" {
		priority = string(domain.PriorityMedium)
	}
	if !validPriority(priority) {
		return domain.Node{}, fmt.Errorf("invalid priority %q", priority)
	}
	// type 定義由 node_types 查表（新增 type＝INSERT 一筆，不必改 Go switch／schema）。
	reg, err := s.typeRegistry(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	id := in.ID
	if id == "" {
		// 用台北時間算 ID 內的日期（時間統一以台北為準）。
		gen, err := reg.GenerateID(in.Type, in.ParentID, in.Title, owner, now.In(taipei))
		if err != nil {
			return domain.Node{}, err
		}
		id = gen
	}
	if err := reg.ValidateID(in.Type, in.ParentID, id); err != nil {
		return domain.Node{}, err
	}

	node := domain.Node{
		ID:        id,
		Title:     in.Title,
		Body:      in.Body,
		Tags:      in.Tags,
		Type:      in.Type,
		ParentID:  in.ParentID,
		Status:    domain.StatusTodo,
		Owner:     owner,
		Priority:  domain.Priority(priority),
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if in.ParentID != "" {
			ok, err := nodeExists(ctx, tx, in.ParentID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("parent %q: %w", in.ParentID, ErrNotFound)
			}
			// parent 型別檢查泛化：查 node_types.parent_type（domain 只驗 id 字串形狀，這條由 store 驗）。
			// parent_type 非空＝parent 必須是該 type；空＝可掛 project 下，不設限。
			def, ok := reg.Lookup(in.Type)
			if !ok {
				return fmt.Errorf("type %q: %w", in.Type, domain.ErrInvalidID)
			}
			if def.ParentType != "" {
				pt, err := parentType(ctx, tx, in.ParentID)
				if err != nil {
					return err
				}
				if pt != def.ParentType {
					// item 保留既有 sentinel（CLI 已對它做中文訊息映射，見 cmd/pb/errors.go）。
					if in.Type == domain.TypeItem {
						return fmt.Errorf("item 的 parent %q 是 %s: %w", in.ParentID, pt, ErrItemParentNotReq)
					}
					return fmt.Errorf("%s 的 parent %q 是 %s，需為 %s: %w",
						in.Type, in.ParentID, pt, def.ParentType, ErrParentTypeMismatch)
				}
			}
		}
		ok, err := nodeExists(ctx, tx, id)
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("node %q: %w", id, ErrIDExists)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO nodes (id, type, parent_id, title, status, owner, priority, tags, body, sort, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			node.ID, string(node.Type), nullIfEmpty(node.ParentID), node.Title, string(node.Status),
			node.Owner, string(node.Priority), node.Tags, node.Body, node.Sort,
			formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("insert node %q: %w", id, err)
		}
		// create 事件（DATA_MODEL.md §9 範例：field／from→to 空，note 記「建立 <id>」）
		return insertHistory(ctx, tx, now, historyEntry{
			nodeID: id, actor: actor, action: domain.ActionCreate, note: "建立 " + id,
		})
	})
	if err != nil {
		return domain.Node{}, err
	}
	return node, nil
}

// nullIfEmpty：根節點的 parent_id 寫 NULL（DDL 用 FK＋ON DELETE RESTRICT）。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Update 部分更新；expectedUpdatedAt 非 nil 時做樂觀鎖（不符回 ErrConflict，不寫入、不留 history）。
// 沒有任何欄位真的變動時＝no-op：不寫 DB、不留 history、updated_at 不動。
func (s *Store) Update(ctx context.Context, actor, id string, in UpdateInput, expectedUpdatedAt *time.Time) (domain.Node, error) {
	if err := validateActor(actor); err != nil {
		return domain.Node{}, err
	}
	var out domain.Node
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getNodeQ(ctx, tx, id)
		if err != nil {
			return err
		}
		if expectedUpdatedAt != nil && !cur.UpdatedAt.Equal(*expectedUpdatedAt) {
			return fmt.Errorf("node %q: %w", id, ErrConflict)
		}
		upd := cur
		var changes []fieldChange
		if in.Title != nil && *in.Title != cur.Title {
			if strings.TrimSpace(*in.Title) == "" {
				return errors.New("update: title cannot be empty")
			}
			upd.Title = *in.Title
			changes = append(changes, fieldChange{"title", cur.Title, *in.Title})
		}
		if in.Body != nil && *in.Body != cur.Body {
			upd.Body = *in.Body
			changes = append(changes, fieldChange{"body", cur.Body, *in.Body})
		}
		if in.Owner != nil && *in.Owner != cur.Owner {
			if !domain.IsValidOwner(*in.Owner) {
				return fmt.Errorf("owner %q: %w", *in.Owner, domain.ErrInvalidOwner)
			}
			upd.Owner = *in.Owner
			changes = append(changes, fieldChange{"owner", cur.Owner, *in.Owner})
		}
		if in.Priority != nil && *in.Priority != string(cur.Priority) {
			if !validPriority(*in.Priority) {
				return fmt.Errorf("invalid priority %q", *in.Priority)
			}
			upd.Priority = domain.Priority(*in.Priority)
			changes = append(changes, fieldChange{"priority", string(cur.Priority), *in.Priority})
		}
		if in.Tags != nil && *in.Tags != cur.Tags {
			upd.Tags = *in.Tags
			changes = append(changes, fieldChange{"tags", cur.Tags, *in.Tags})
		}
		if in.Sort != nil && *in.Sort != cur.Sort {
			upd.Sort = *in.Sort
			changes = append(changes, fieldChange{"sort", fmt.Sprint(cur.Sort), fmt.Sprint(*in.Sort)})
		}
		if len(changes) == 0 {
			out = cur
			return nil
		}
		now := nowSec()
		upd.UpdatedAt = now
		if _, err := tx.ExecContext(ctx,
			`UPDATE nodes SET title = ?, body = ?, owner = ?, priority = ?, tags = ?, sort = ?, updated_at = ?
			 WHERE id = ?`,
			upd.Title, upd.Body, upd.Owner, string(upd.Priority), upd.Tags, upd.Sort, formatTime(now), id); err != nil {
			return fmt.Errorf("update node %q: %w", id, err)
		}
		for _, c := range changes {
			if err := insertHistory(ctx, tx, now, historyEntry{
				nodeID: id, actor: actor, action: domain.ActionUpdate, field: c.field, from: c.from, to: c.to,
			}); err != nil {
				return err
			}
		}
		out = upd
		return nil
	})
	if err != nil {
		return domain.Node{}, err
	}
	return out, nil
}

// Transition 狀態流轉（狀態機權威：DATA_MODEL.md §6）。
//   - from==to：**no-op**——直接回目前節點（不寫、不留 history、不檢查 note／expectedUpdatedAt）。
//     domain.IsValidTransition 對相同狀態一律回 false，那份 doc 載明「呼叫端自己處理 no-op」，store 就是那個呼叫端。
//   - 轉入 blocked：note 非空**或**已有 depends_on link，否則回 domain.ErrMissingBlockReason（§11.3）。
//   - done → in_progress（reopen）：§6 載明需 note。
func (s *Store) Transition(ctx context.Context, actor, id string, to domain.Status, note string, expectedUpdatedAt *time.Time) (domain.Node, error) {
	if err := validateActor(actor); err != nil {
		return domain.Node{}, err
	}
	var out domain.Node
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getNodeQ(ctx, tx, id)
		if err != nil {
			return err
		}
		from := cur.Status
		if from == to {
			out = cur
			return nil
		}
		if expectedUpdatedAt != nil && !cur.UpdatedAt.Equal(*expectedUpdatedAt) {
			return fmt.Errorf("node %q: %w", id, ErrConflict)
		}
		if !domain.IsValidTransition(from, to) {
			return fmt.Errorf("node %q: %s → %s: %w", id, from, to, domain.ErrIllegalTransition)
		}
		if domain.RequiresBlockReason(to) && strings.TrimSpace(note) == "" {
			has, err := hasDependsOnLink(ctx, tx, id)
			if err != nil {
				return err
			}
			if !has {
				return fmt.Errorf("node %q: %w", id, domain.ErrMissingBlockReason)
			}
		}
		if from == domain.StatusDone && to == domain.StatusInProgress && strings.TrimSpace(note) == "" {
			return fmt.Errorf("node %q: reopen (done → in_progress) requires note", id)
		}
		now := nowSec()
		if _, err := tx.ExecContext(ctx,
			"UPDATE nodes SET status = ?, updated_at = ? WHERE id = ?", string(to), formatTime(now), id); err != nil {
			return fmt.Errorf("transition node %q: %w", id, err)
		}
		if err := insertHistory(ctx, tx, now, historyEntry{
			nodeID: id, actor: actor, action: domain.ActionTransition,
			field: "status", from: string(from), to: string(to), note: note,
		}); err != nil {
			return err
		}
		out = cur
		out.Status = to
		out.UpdatedAt = now
		return nil
	})
	if err != nil {
		return domain.Node{}, err
	}
	return out, nil
}

// Assign 換手；owner 與現值相同時 no-op（不寫、不留 history）。
func (s *Store) Assign(ctx context.Context, actor, id, owner string) (domain.Node, error) {
	if err := validateActor(actor); err != nil {
		return domain.Node{}, err
	}
	if !domain.IsValidOwner(owner) {
		return domain.Node{}, fmt.Errorf("owner %q: %w", owner, domain.ErrInvalidOwner)
	}
	var out domain.Node
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getNodeQ(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Owner == owner {
			out = cur
			return nil
		}
		now := nowSec()
		if _, err := tx.ExecContext(ctx,
			"UPDATE nodes SET owner = ?, updated_at = ? WHERE id = ?", owner, formatTime(now), id); err != nil {
			return fmt.Errorf("assign node %q: %w", id, err)
		}
		if err := insertHistory(ctx, tx, now, historyEntry{
			nodeID: id, actor: actor, action: domain.ActionAssign,
			field: "owner", from: cur.Owner, to: owner,
		}); err != nil {
			return err
		}
		out = cur
		out.Owner = owner
		out.UpdatedAt = now
		return nil
	})
	if err != nil {
		return domain.Node{}, err
	}
	return out, nil
}

// Verify：只對 status=review 的節點做，成功後 status=done；actor==owner 時 note 前綴 "[self-verified] "（§11.2）。
func (s *Store) Verify(ctx context.Context, actor, id, note string) (domain.Node, error) {
	if err := validateActor(actor); err != nil {
		return domain.Node{}, err
	}
	if strings.TrimSpace(note) == "" {
		return domain.Node{}, errors.New("verify: note (evidence) is required")
	}
	var out domain.Node
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getNodeQ(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status != domain.StatusReview {
			return fmt.Errorf("node %q (status=%s): %w", id, cur.Status, ErrNotInReview)
		}
		noteVal := note
		if domain.IsSelfVerified(actor, cur.Owner) {
			noteVal = "[self-verified] " + note
		}
		now := nowSec()
		if _, err := tx.ExecContext(ctx,
			"UPDATE nodes SET status = ?, updated_at = ? WHERE id = ?",
			string(domain.StatusDone), formatTime(now), id); err != nil {
			return fmt.Errorf("verify node %q: %w", id, err)
		}
		if err := insertHistory(ctx, tx, now, historyEntry{
			nodeID: id, actor: actor, action: domain.ActionVerify,
			field: "status", from: string(domain.StatusReview), to: string(domain.StatusDone), note: noteVal,
		}); err != nil {
			return err
		}
		out = cur
		out.Status = domain.StatusDone
		out.UpdatedAt = now
		return nil
	})
	if err != nil {
		return domain.Node{}, err
	}
	return out, nil
}

// Comment 只寫 history（不動節點欄位，也不改 updated_at）。
func (s *Store) Comment(ctx context.Context, actor, id, text string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("comment: text is required")
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := getNodeQ(ctx, tx, id); err != nil {
			return err
		}
		return insertHistory(ctx, tx, nowSec(), historyEntry{
			nodeID: id, actor: actor, action: domain.ActionComment, note: text,
		})
	})
}

// Delete：僅 report 或「無子節點且無 link」的節點可刪，否則回 ErrCannotDelete。
//
// 註：節點刪除後，其 history／出向 links 依 DATA_MODEL.md §2/§4 的 ON DELETE CASCADE 一併移除
// （DDL 為權威）。節點已不存在，故刪除本身不留 history；要留痕需改軟刪（v0.2）。
// 「被 depends_on 指到」的節點也視為有 link 而不可刪，避免卡點清單指向已刪節點（§11.4 精神）。
func (s *Store) Delete(ctx context.Context, actor, id string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getNodeQ(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Type != domain.TypeReport {
			counts := []struct {
				query string
				arg   string
			}{
				{"SELECT COUNT(*) FROM nodes WHERE parent_id = ?", id},
				{"SELECT COUNT(*) FROM links WHERE from_id = ?", id},
				{"SELECT COUNT(*) FROM links WHERE kind = 'depends_on' AND target = ?", id},
			}
			for _, c := range counts {
				var n int
				if err := tx.QueryRowContext(ctx, c.query, c.arg).Scan(&n); err != nil {
					return fmt.Errorf("delete check %q: %w", id, err)
				}
				if n > 0 {
					return fmt.Errorf("node %q: %w", id, ErrCannotDelete)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id); err != nil {
			return fmt.Errorf("delete node %q: %w", id, err)
		}
		return nil
	})
}
