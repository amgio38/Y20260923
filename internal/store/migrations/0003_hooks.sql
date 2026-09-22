-- ProjectBoard schema v3：harness hook 訂閱（Y20260920/REQ-V03-HOOK 裁示，2026-09-20）
--   - 訂閱存在 SQLite，重啟還在（PO 裁示）。
--   - 這輪 harness 只收 herdr（其餘在 store 就先擋）。
--   - 節點被刪時，訂閱一起走（FK ON DELETE CASCADE，與 links 同慣例）。
--   - 喚醒游標放 meta（k='hook_cursor'），由 serve 每 2 秒推進，不另開表。

CREATE TABLE hooks (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  actor      TEXT NOT NULL,                     -- 訂閱者（名冊內）；用來判斷「自己改的不叫自己」
  harness    TEXT NOT NULL CHECK (harness IN ('herdr')),
  target     TEXT NOT NULL,                     -- agent 名（^[a-z][a-z0-9_-]{0,31}$）
  created_at TEXT NOT NULL,
  UNIQUE (node_id, harness, target)             -- 同一節點同一目標不重複；重複訂閱＝更新訂閱者
);
CREATE INDEX idx_hooks_node ON hooks(node_id);
