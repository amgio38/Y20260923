-- ProjectBoard schema v5：links.kind 新增 'repo'（專案↔repo；Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-REPO，2026-09-20）
--
-- 為什麼要直接改 sqlite_master（同 0004_item）：
--   1. links.kind 的 CHECK 是 0001_init 建的，SQLite 不能 ALTER 掉既有約束。
--   2. 重建表在 foreign_keys=ON 時會讓 ON DELETE CASCADE 帶走關聯／history（PRAGMA foreign_keys
--      在交易內是 no-op，關不掉）→ 資料毀損。
--   3. 用 SQLite 官方做法：開 writable_schema 改 sqlite_master 的 DDL，再靠 bumpSchemaCookie
--      推一格強制所有連線重載 schema。
-- 護欄：同交易用 CHECK 表驗證字串真的換到了，換不到就整筆回滾。
--
-- 只放寬 CHECK（多收 'repo'），不動欄位、不動資料、不動 trigger。

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        'CHECK (kind IN (''depends_on'',''file'',''commit'',''pr'',''url'',''doc''))',
        'CHECK (kind IN (''depends_on'',''file'',''commit'',''pr'',''url'',''doc'',''repo''))')
 WHERE type = 'table' AND name = 'links';

PRAGMA writable_schema = OFF;

-- 護欄：links 的 DDL 沒含 'repo' 就寫入 0 → CHECK 失敗 → 整筆 migration 回滾。
CREATE TABLE repo_link_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO repo_link_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'links') LIKE '%''repo''%'
              THEN 1 ELSE 0 END;
DROP TABLE repo_link_migration_guard;
