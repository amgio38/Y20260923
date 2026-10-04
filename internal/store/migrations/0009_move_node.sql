-- ProjectBoard schema v9：搬單功能（Y20260920/REQ-MOVE-NODE/ISSUE-MOVE-NODE-CORE，2026-10-05）
--
-- 內容：
--   1. 建 id_aliases：舊 id → 現行 id。節點搬移（改寫 id）後，舊 id 仍由 Get 解析到新位置。
--   2. 放寬 history.action 的 CHECK，多收 'move'（搬移事件）。
--
-- 為什麼改 sqlite_master（同 0004_item／0005_repo_link／0007_history_actions）：
--   1. history.action 的 CHECK 是 0001_init 建的，SQLite 不能 ALTER 掉既有約束。
--   2. 重建表在 foreign_keys=ON 時會讓 ON DELETE CASCADE 帶走關聯／history
--      （PRAGMA foreign_keys 在交易內是 no-op，關不掉）→ 資料毀損。
--   3. 用 SQLite 官方做法：開 writable_schema 改 sqlite_master 的 DDL，
--      靠 migrate.go 的 bumpSchemaCookie 推一格強制所有連線重載 schema。
-- 護欄：同交易用 CHECK 表驗證字串真的換到了，換不到就整筆回滾。
--
-- 只放寬 CHECK（多收 'move'）＋新增一張表；不動任何欄位、不動既有資料、不動 trigger。

CREATE TABLE id_aliases (
  old_id   TEXT PRIMARY KEY,                 -- 搬移前的舊 id（路徑式）
  new_id   TEXT NOT NULL,                    -- 現行 id（節點再被搬時，這裡會持續指向最新位置）
  moved_at TEXT NOT NULL,                    -- ISO8601（台北）
  actor    TEXT NOT NULL                     -- 執行搬移者（owner 名冊內）
);
CREATE INDEX idx_id_aliases_new ON id_aliases(new_id);

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        '(''create'',''update'',''transition'',''assign'',''link'',''unlink'',''comment'',''verify'',''delete'')',
        '(''create'',''update'',''transition'',''assign'',''link'',''unlink'',''comment'',''verify'',''delete'',''move'')')
 WHERE type = 'table' AND name = 'history';

PRAGMA writable_schema = OFF;

-- 護欄：history 的 DDL 沒含 'move' 就寫入 0 → CHECK 失敗 → 整筆 migration 回滾。
CREATE TABLE history_move_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO history_move_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'history') LIKE '%''move''%'
              THEN 1 ELSE 0 END;
DROP TABLE history_move_migration_guard;
