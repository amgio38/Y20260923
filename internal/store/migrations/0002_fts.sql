-- ProjectBoard schema v2：全文搜尋索引（FTS5 ＋ trigram tokenizer）。
-- 來源：Y20260920/REQ-V02-FTS 的 CTO 裁示（2026-09-20）：trigram 讓中文子字串查得到，
-- migration 遞增 schema_version，既有節點要進索引（下方 INSERT…SELECT 一次補齊）。
--
-- 為什麼是獨立表（不用 content='nodes' 的外部內容表）：nodes 的 rowid 是隱含的，
-- VACUUM 有機會重編號；用 node_id 自己當鍵，索引與 nodes 的 rowid 解耦。
-- 同步一律交給 trigger：任何寫入路徑（CLI／MCP／import）都不必自己維護索引。

CREATE VIRTUAL TABLE nodes_fts USING fts5(
  node_id UNINDEXED,
  title,
  body,
  tags,
  tokenize = 'trigram'
);

INSERT INTO nodes_fts (node_id, title, body, tags)
  SELECT id, title, body, tags FROM nodes;

CREATE TRIGGER nodes_fts_ai AFTER INSERT ON nodes BEGIN
  INSERT INTO nodes_fts (node_id, title, body, tags) VALUES (new.id, new.title, new.body, new.tags);
END;

CREATE TRIGGER nodes_fts_ad AFTER DELETE ON nodes BEGIN
  DELETE FROM nodes_fts WHERE node_id = old.id;
END;

CREATE TRIGGER nodes_fts_au AFTER UPDATE ON nodes BEGIN
  DELETE FROM nodes_fts WHERE node_id = old.id;
  INSERT INTO nodes_fts (node_id, title, body, tags) VALUES (new.id, new.title, new.body, new.tags);
END;
