package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"project_board/internal/store"
)

// maxMessageBytes 是單則 JSON-RPC 上限（10MB；board 全量 tree 也遠小於此）。
const maxMessageBytes = 10 << 20

// ---------------------------------------------------------------------------
// stdio（MCP 標準傳輸；自動相容「Content-Length framing」與「逐行 JSON」兩種
// client 傳輸協定）
//
// MCP 官方 stdio 規格（PB-NFR-09）是「Content-Length: <n>\r\n\r\n<body>」
// frame。實務上兩代 client 不同調：
//   - 舊 SDK／opencode 等 harness：用 Content-Length framing 收發；
//   - 新 SDK（@modelcontextprotocol/sdk 新版）：stdio 改走逐行 JSON
//     （serializeMessage = JSON.stringify(...)+"\n"，readMessage 以 \n 切行）。
//
// 第一筆輸入是 frame 就鎖定 frame 輸出；是一般 JSON 行就鎖定逐行 JSON 輸出，
// 兩邊都活、舊版相容不變。
// ---------------------------------------------------------------------------

func serveStdio(ctx context.Context, st *store.Store, r io.Reader, w io.Writer) error {
	srv := &Server{st: st}
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	framed := false // 輸出協定：false＝逐行 JSON，true＝Content-Length frame
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, wasFrame, err := readMessage(br, maxMessageBytes)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if msg == "" {
			continue
		}
		if wasFrame {
			framed = true
		}
		if out := serveLine(ctx, srv, msg); out != "" {
			if framed {
				if err := writeMessage(bw, out); err != nil {
					return err
				}
			} else {
				if _, err := bw.WriteString(out + "\n"); err != nil {
					return err
				}
				if err := bw.Flush(); err != nil {
					return err
				}
			}
		}
	}
}

// writeMessage 以 MCP 標準 Content-Length frame 寫出一則回應。
func writeMessage(bw *bufio.Writer, payload string) error {
	b := []byte(payload)
	if _, err := fmt.Fprintf(bw, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	if _, err := bw.Write(b); err != nil {
		return err
	}
	return bw.Flush()
}

// readMessage 讀一則 JSON-RPC：優先解標準 Content-Length frame；
// 非 frame 開頭的行（新 SDK／舊逐行 JSON）直接整行當一則。
// wasFrame 回傳該筆是否來自 Content-Length frame（呼叫端據此鎖定輸出協定）。
func readMessage(br *bufio.Reader, max int) (string, bool, error) {
	line, err := br.ReadString('\n')
	if len(line) == 0 {
		return "", false, err // io.EOF 或讀不到任何 byte
	}
	if err != nil && err != io.EOF {
		return "", false, err
	}
	header := strings.TrimSpace(line)
	if !strings.HasPrefix(strings.ToLower(header), "content-length:") {
		return header, false, nil // 逐行 JSON（新 SDK／舊相容）
	}
	// 欄位值從 ':' 後切（大小寫都吃：Content-Length / content-length）。
	idx := strings.IndexByte(header, ':')
	n, err := strconv.Atoi(strings.TrimSpace(header[idx+1:]))
	if err != nil || n < 0 || n > max {
		return "", true, fmt.Errorf("invalid Content-Length frame: %q", header)
	}
	// 空行分隔符（\r\n 或 \n）。
	if _, err := br.ReadString('\n'); err != nil {
		return "", true, err
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return "", true, err
	}
	return string(body), true, nil
}

// serveLine：單則回一則（notification 回空字串＝不回）；
// '[' 開頭當 batch，逐則處理包成 array（全 notification 則不回）。
func serveLine(ctx context.Context, srv *Server, line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "[") {
		var msgs []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &msgs); err != nil {
			out, _ := json.Marshal(errResp(nil, -32700, "parse error: "+err.Error()))
			return string(out)
		}
		var resps []json.RawMessage
		for _, m := range msgs {
			if out := srv.handleRaw(ctx, m); out != nil {
				resps = append(resps, out)
			}
		}
		if len(resps) == 0 {
			return ""
		}
		out, _ := json.Marshal(resps)
		return string(out)
	}
	if out := srv.handleRaw(ctx, json.RawMessage(trimmed)); out != nil {
		return string(out)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Streamable HTTP（最小可用：只吃 POST；掛在 pb serve 的 /mcp 下）
// ---------------------------------------------------------------------------

type httpHandler struct {
	srv *Server
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// v0.1 不做 SSE 串流：GET 回 405 明講，不假裝支援。
		http.Error(w, "mcp streamable http: use POST (SSE not implemented in v0.1)", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageBytes+1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	out := serveLine(r.Context(), h.srv, strings.TrimSpace(string(body)))
	if out == "" {
		w.WriteHeader(http.StatusAccepted) // 純 notification：202 無 body
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(out))
}
