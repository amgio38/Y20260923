-- ProjectBoard schema v6：node type 資料驅動（Y20260920/REQ-V05-NODE-TYPE-REGISTRY，2026-09-22）
--
-- 目的：把「type 有哪些」從結構（nodes.type 的 CHECK enum）搬成資料（node_types 表）。
-- 之後新增／修改 type ＝ INSERT／UPDATE 一筆 node_types，不必再動 schema、不必改 Go switch。
--
-- 內容：
--   1. 建 node_types 表並 seed 7 筆（既有 5 種 ＋ bug／plan 兩種）。
--   2. nodes.type 的 CHECK (type IN (...)) 換成 REFERENCES node_types(key)——這是最後一次動 schema。
--
-- 為什麼要直接改 sqlite_master（同 0004_item／0005_repo_link）：
--   1. nodes.type 的 CHECK 是 0001_init 建的（0004 放寬過一次），SQLite 不能 ALTER 掉既有約束。
--   2. 重建表在 foreign_keys=ON 時會讓 ON DELETE CASCADE／RESTRICT 帶走資料
--      （PRAGMA foreign_keys 在交易內是 no-op，關不掉）→ 資料毀損。
--   3. 用 SQLite 官方做法：開 writable_schema 改 sqlite_master 的 DDL，再靠 migrate.go 的
--      bumpSchemaCookie 推一格強制所有連線重載 schema。
-- 護欄：同交易用 CHECK 表驗證字串真的換到了，換不到就整筆回滾。
--
-- 註：node_types 先建好，nodes 的 FK 才有母表可指（外鍵於寫入時檢查）。
--     id_shape 的形狀邏輯留在 Go（domain 的固定形狀庫），本表只記「哪個 type 用哪個形狀」。

CREATE TABLE node_types (
  key         TEXT PRIMARY KEY,                 -- type 代號
  id_prefix   TEXT NOT NULL,                    -- ID 慣用前綴（project 為空字串）
  id_shape    TEXT NOT NULL,                    -- 形狀識別字（project_date／slug／item_key／report_owner_date）
  label       TEXT NOT NULL,                    -- 顯示名稱
  parent_type TEXT NOT NULL DEFAULT '',         -- 非空＝parent 必須是該 type；空＝可掛 project 下
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL
);

INSERT INTO node_types (key, id_prefix, id_shape, label, parent_type, sort, created_at) VALUES
  ('project', '',       'project_date',      '專案',     '',    1, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('req',     'REQ',    'slug',              '需求',     '',    2, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('issue',   'ISSUE',  'slug',              '議題',     '',    3, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('report',  'REPORT', 'report_owner_date', '報告',     '',    4, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('item',    'ITEM',   'item_key',          '母表項目', 'req', 5, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('bug',     'BUG',    'slug',              'BUG',      'req', 6, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours')),
  ('plan',    'PLAN',   'slug',              '計畫',     '',    7, strftime('%Y-%m-%dT%H:%M:%S+08:00','now','+8 hours'));

PRAGMA writable_schema = ON;

UPDATE sqlite_master
   SET sql = replace(sql,
        'CHECK (type IN (''project'',''req'',''issue'',''report'',''item''))',
        'REFERENCES node_types(key)')
 WHERE type = 'table' AND name = 'nodes';

PRAGMA writable_schema = OFF;

-- 護欄：nodes 的 DDL 沒含 'REFERENCES node_types(key)' 就寫入 0 → CHECK 失敗 → 整筆回滾。
CREATE TABLE node_types_migration_guard (ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO node_types_migration_guard (ok)
  SELECT CASE WHEN (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'nodes')
                   LIKE '%REFERENCES node_types(key)%'
              THEN 1 ELSE 0 END;
DROP TABLE node_types_migration_guard;
