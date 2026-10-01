-- ProjectBoard schema v7：history.action 新增 'delete'（即時推送事件來源；
-- Y20260920/REQ-DASHBOARD-LIVE-UPDATES/ISSUE-LIVE-B1-HISTORY-COVERAGE，
-- CTO 2026-09-25 裁定：只放寬 CHECK、只加 'delete' 一個值）。
--
-- 為什麼要直接改 sqlite_master（同 0004_item／0005_repo_link）：
--   1. history.action 的 CHECK 是 0001_init 建的，SQLite 不能 ALTER 掉既有約束。
--   2. 重建表在 foreign_keys=ON 時會讓 ON DELETE CASCADE 帶走資料（PRAGMA foreign_keys
--      在交易內是 no-op，關不掉）→ 資料毀損；放寬 CHECK 不需要重建表。
--   3. 用 SQLite 官方做法：開 writable_schema 改 sqlite_master 的 DDL，再靠 bumpSchemaCookie
--      推一格強制所有連線重載 schema。
-- 護欄：同交易用 CHECK 表驗證字串真的換到了，換不到就整筆回滾。
--
-- 只放寬 CHECK（多收 'delete'），不動欄位、不動資料、不動 trigger；舊 DB 的 history
-- 一筆都不會少。

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        '(''create'',''update'',''transition'',''assign'',''link'',''unlink'',''comment'',''verify'')',
        '(''create'',''update'',''transition'',''assign'',''link'',''unlink'',''comment'',''verify'',''delete'')')
 WHERE type = 'table' AND name = 'history';

PRAGMA writable_schema = OFF;

-- 護欄：history 的 DDL 沒含 'delete' 就寫入 0 → CHECK 失敗 → 整筆 migration 回滾。
CREATE TABLE history_actions_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO history_actions_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'history') LIKE '%''delete''%'
              THEN 1 ELSE 0 END;
DROP TABLE history_actions_migration_guard;
