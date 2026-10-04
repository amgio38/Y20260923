// Package version：單一版號來源。CLI（pb version）／MCP（initialize 的 serverInfo.version）／
// REST（/healthz）都從這裡讀，不要各自重複寫一份。
package version

// Version 格式 V0.YYYYMMDD.NNN；升到穩定版才改 V1。
// 2026-10-05：搬單功能（pb reparent／pb_move／MoveNode，schema v9）落地 → 由 09-28 升上來。
const Version = "V0.20261005.001"
