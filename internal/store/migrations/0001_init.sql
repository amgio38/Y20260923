-- ProjectBoard schema v1
-- 來源：docs/DATA_MODEL.md §2~§5（欄位／型別／CHECK／索引一律照抄，不自行改欄位）。
-- 套用者：internal/store/migrate.go 的 Migrate()，每筆 migration 一個 transaction。

CREATE TABLE nodes (
  id         TEXT PRIMARY KEY,                 -- 人類可讀 ID（路徑式）
  type       TEXT NOT NULL CHECK (type IN ('project','req','issue','report')),
  parent_id  TEXT REFERENCES nodes(id) ON DELETE RESTRICT,   -- 根為 NULL
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'todo'
             CHECK (status IN ('todo','in_progress','review','blocked','hold','done','cancel')),
  owner      TEXT NOT NULL DEFAULT 'unassigned',
  priority   TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('high','medium','low')),
  tags       TEXT NOT NULL DEFAULT '',          -- 逗號分隔
  body       TEXT NOT NULL DEFAULT '',          -- markdown 正文
  sort       INTEGER NOT NULL DEFAULT 0,        -- 同層排序
  created_at TEXT NOT NULL,                     -- ISO8601（台北）
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_nodes_parent      ON nodes(parent_id);
CREATE INDEX idx_nodes_type_status ON nodes(type, status);
CREATE INDEX idx_nodes_owner       ON nodes(owner);

CREATE TABLE links (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  from_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('depends_on','file','commit','pr','url','doc')),
  target     TEXT NOT NULL,                     -- depends_on→節點 id；其餘→字串
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_links_from   ON links(from_id);
CREATE INDEX idx_links_target ON links(target);

CREATE TABLE history (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id  TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  ts       TEXT NOT NULL,                       -- ISO8601
  actor    TEXT NOT NULL,
  action   TEXT NOT NULL CHECK (action IN
             ('create','update','transition','assign','link','unlink','comment','verify')),
  field    TEXT NOT NULL DEFAULT '',
  from_val TEXT NOT NULL DEFAULT '',
  to_val   TEXT NOT NULL DEFAULT '',
  note     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_history_node ON history(node_id, id);

CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
