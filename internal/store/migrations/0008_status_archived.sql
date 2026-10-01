-- ProjectBoard schema v8：新增 status='archived'（Y20260920/REQ-ARCHIVED-DONE，2026-09-28）
--
-- 目的：done 單驗證無誤後可手動封存（archived），跟 cancel（不做/放棄）語意分開；
-- archived 可轉回 done（unarchive）。狀態機規則本身在 internal/domain/domain.go，
-- 本檔只放寬資料庫層的 CHECK 約束。
--
-- 為什麼要直接改 sqlite_master（同 0004_item／0005_repo_link／0006_node_types）：
--   1. nodes.status 的 CHECK 是 0001_init 建的，SQLite 不能 ALTER 掉既有約束。
--   2. 重建表在 foreign_keys=ON 時會讓 ON DELETE CASCADE／RESTRICT 帶走資料
--      （PRAGMA foreign_keys 在交易內是 no-op，關不掉）→ 資料毀損。
--   3. 用 SQLite 官方做法：開 writable_schema 改 sqlite_master 的 DDL，
--      靠 schema cookie 變動（guard 表 create／drop）強制所有連線重載 schema。
-- 護欄：同交易用 CHECK 表驗證字串真的換到了，換不到就整筆回滾。
--
-- 注意：這裡只放寬 CHECK（多收 'archived'），不動其他欄位、不動既有資料、不動 trigger。

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        'CHECK (status IN (''todo'',''in_progress'',''review'',''blocked'',''hold'',''done'',''cancel''))',
        'CHECK (status IN (''todo'',''in_progress'',''review'',''blocked'',''hold'',''done'',''cancel'',''archived''))')
 WHERE type = 'table' AND name = 'nodes';

PRAGMA writable_schema = OFF;

-- 護欄：nodes 的 DDL 沒含 'archived' 就寫入 0 → CHECK 失敗 → 整筆 migration 回滾。
CREATE TABLE status_archived_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO status_archived_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'nodes') LIKE '%''archived''%'
              THEN 1 ELSE 0 END;
DROP TABLE status_archived_migration_guard;
