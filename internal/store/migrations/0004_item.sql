-- ProjectBoard schema v4：新增 type=item（母表項目；Y20260920/REQ-V03-CHECKLIST 裁示，2026-09-20）
--
-- 母表只做索引，不複製 ISSUE 正文：一列 item 用 depends_on 指向已存在的 issue；
-- item 的 id 固定為 <parent(req)>/ITEM-<KEY>，KEY 是母表編號（A1、B3、K12）。
--
-- 為什麼要直接改 sqlite_master：
--   1. nodes.type 的 CHECK 是 0001_init 建的，SQLite 不能 ALTER 掉既有約束。
--   2. 「重建表」（建新表→搬資料→DROP 舊表→改名）在 foreign_keys=ON 時，DROP TABLE 會先做隱式
--      DELETE FROM，links／history 的 ON DELETE CASCADE 會把整批關聯與事件帶走 → 資料毀損。
--      而 PRAGMA foreign_keys 在交易內是 no-op（migrate 一律包在交易裡），關不掉。
--   3. 因此用 SQLite 官方文件的做法：開 writable_schema 改 sqlite_master 裡的 DDL 字串，
--      再讓「schema cookie 變動」強制所有連線重載 schema（下面的 guard 表 create／drop 就是那個變動）。
-- 護欄：同一筆交易內用一個 CHECK 表驗證字串真的換到了；換不到就讓 migration 失敗、整筆回滾。
--
-- 注意：這裡只放寬 CHECK（多收 'item'），不動任何欄位、不動資料、不動 trigger。

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        'CHECK (type IN (''project'',''req'',''issue'',''report''))',
        'CHECK (type IN (''project'',''req'',''issue'',''report'',''item''))')
 WHERE type = 'table' AND name = 'nodes';

PRAGMA writable_schema = OFF;

-- 護欄：nodes 的 DDL 沒含 'item' 就寫入 0 → CHECK 失敗 → 整筆 migration 回滾。
CREATE TABLE item_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO item_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'nodes') LIKE '%''item''%'
              THEN 1 ELSE 0 END;
DROP TABLE item_migration_guard;
