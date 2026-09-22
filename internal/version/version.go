// Package version：單一版號來源。CLI（pb version）／MCP（initialize 的 serverInfo.version）／
// REST（/healthz）都從這裡讀，不要各自重複寫一份。
package version

// Version 格式 V0.YYYYMMDD.NNN；升到穩定版才改 V1。
const Version = "V0.20260923.001"
