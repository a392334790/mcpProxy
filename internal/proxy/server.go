package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"mcpProxy/internal/config"
	"mcpProxy/internal/session"
)

type Server struct {
	cfg          *config.Config
	session      *session.Manager
	upstream     *http.Client
	upstreamURL  string
	callbackPath string
}

func NewServer(cfg *config.Config, sessionManager *session.Manager) http.Handler {
	s := &Server{
		cfg:          cfg,
		session:      sessionManager,
		upstream:     &http.Client{},
		upstreamURL:  cfg.UpstreamMCPURL,
		callbackPath: cfg.CallbackPath,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc(cfg.CallbackPath, s.handleCallback)
	mux.HandleFunc("/auth/status", s.handleStatus)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/", s.handleRoot)
	return loggingMiddleware(mux)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "mcp-auth-proxy",
		"mcp_endpoint":    "http://" + s.cfg.ListenAddr + "/mcp",
		"status_endpoint": "http://" + s.cfg.ListenAddr + "/auth/status",
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, s.session.Status())
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	login, err := s.session.EnsureLogin(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "login_start_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started":    true,
		"auth_url":   login.AuthURL,
		"opened":     login.Opened,
		"expires_at": login.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := s.session.Logout(); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "logout_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logged_in": false})
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeHTML(w, http.StatusBadRequest, callbackPage("登录失败", "缺少 code 或 state 参数，请返回智能体重试。"))
		return
	}
	_, err := s.session.HandleCallback(r.Context(), code, state)
	if err != nil {
		writeHTML(w, http.StatusBadRequest, callbackPage("登录失败", err.Error()))
		return
	}
	writeHTML(w, http.StatusOK, callbackPage("登录成功", "认证已完成，请返回智能体重新发起 MCP 请求。"))
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_request", "failed to read request body", nil)
		return
	}
	defer r.Body.Close()

	token, login, err := s.session.EnsureToken(r.Context())
	if err != nil {
		if errors.Is(err, session.ErrLoginRequired) {
			writeAuthError(w, http.StatusUnauthorized, "auth_required", "请在浏览器完成登录后重试。", map[string]any{
				"auth_url": login.AuthURL,
				"opened":   login.Opened,
			})
			return
		}
		writeAuthError(w, http.StatusBadGateway, "token_refresh_failed", err.Error(), nil)
		return
	}
	if err := s.forwardMCP(r.Context(), w, r, body, token.AccessToken, true); err != nil {
		log.Printf("forward mcp: %v", err)
		if !wroteHeader(w) {
			writeAuthError(w, http.StatusBadGateway, "proxy_failed", err.Error(), nil)
		}
	}
}

func (s *Server) forwardMCP(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, accessToken string, allowRetry bool) error {
	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.upstreamURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	copyHeaders(proxyReq.Header, r.Header)
	proxyReq.Header.Del("Authorization")
	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.upstream.Do(proxyReq)
	if err != nil {
		return fmt.Errorf("call upstream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		payload, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read upstream 401 body: %w", readErr)
		}
		if allowRetry && isTokenExpired(payload) {
			refreshed, login, err := s.session.Refresh(ctx)
			if err == nil {
				return s.forwardMCP(ctx, w, r, body, refreshed.AccessToken, false)
			}
			if errors.Is(err, session.ErrLoginRequired) {
				writeAuthError(w, http.StatusUnauthorized, "auth_required", "登录已过期，请重新登录后重试。", map[string]any{
					"auth_url": login.AuthURL,
					"opened":   login.Opened,
				})
				return nil
			}
			return fmt.Errorf("refresh after token_expired: %w", err)
		}
		copyResponse(w, resp.Header, resp.StatusCode, bytes.NewReader(payload))
		return nil
	}
	copyResponse(w, resp.Header, resp.StatusCode, resp.Body)
	return nil
}

func isTokenExpired(body []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	errorCode, _ := payload["error"].(string)
	return errorCode == "token_expired"
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponse(w http.ResponseWriter, headers http.Header, status int, body io.Reader) {
	for key, values := range headers {
		if hopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(status)
	if flusher, ok := w.(http.Flusher); ok {
		_, _ = io.Copy(flushWriter{ResponseWriter: w, flusher: flusher}, body)
		return
	}
	_, _ = io.Copy(w, body)
}

func hopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAuthError(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	payload := map[string]any{
		"error":   code,
		"message": message,
	}
	for k, v := range extra {
		payload[k] = v
	}
	writeJSON(w, status, payload)
}

func callbackPage(title, message string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>%s</title>
  <style>
    body { font-family: Arial, sans-serif; background:#f5f7fb; color:#1f2937; padding:40px; }
    .card { max-width:560px; margin:0 auto; background:white; border-radius:12px; padding:32px; box-shadow:0 10px 30px rgba(0,0,0,.08); }
    h1 { margin-top:0; }
  </style>
</head>
<body>
  <div class="card">
    <h1>%s</h1>
    <p>%s</p>
  </div>
</body>
</html>`, title, title, message)
}

func writeHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

type statusWriter interface {
	Status() int
	Written() bool
}

func wroteHeader(w http.ResponseWriter) bool {
	sw, ok := w.(statusWriter)
	return ok && sw.Written()
}

type responseRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.written = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) Status() int   { return r.status }
func (r *responseRecorder) Written() bool { return r.written }

func (r *responseRecorder) Flush() {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type flushWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.flusher.Flush()
	}
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d", r.Method, r.URL.Path, rec.Status())
	})
}
