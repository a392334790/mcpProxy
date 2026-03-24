package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

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

type apiResponse struct {
	Success        bool           `json:"success"`
	Code           string         `json:"code"`
	Message        string         `json:"message"`
	DisplayMessage string         `json:"display_message"`
	TraceID        string         `json:"trace_id"`
	Timestamp      time.Time      `json:"timestamp"`
	Data           map[string]any `json:"data"`
}

type upstreamError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
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
		"name":              "mcp-auth-proxy",
		"mcp_endpoint":      s.absoluteURL("/mcp"),
		"status_endpoint":   s.absoluteURL("/auth/status"),
		"login_endpoint":    s.absoluteURL("/auth/login"),
		"logout_endpoint":   s.absoluteURL("/auth/logout"),
		"callback_endpoint": s.absoluteURL(s.cfg.CallbackPath),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r, http.MethodGet)
		return
	}
	status := s.session.Status()
	data := map[string]any{
		"logged_in":     status.LoggedIn,
		"pending_login": status.PendingLogin,
		"status_url":    s.absoluteURL("/auth/status"),
	}
	code := "ok"
	message := "Login status loaded"
	display := "未登录"
	if status.LoggedIn {
		display = "已登录"
		data["user_id"] = status.UserID
		data["user_name"] = status.UserName
		data["scope"] = status.Scope
		data["expires_at"] = status.ExpiresAt
	}
	if status.PendingLogin {
		code = "login_in_progress"
		message = "Login is in progress"
		display = "检测到登录流程正在进行，请在浏览器完成登录后重试。"
		data["auth_url"] = status.AuthURL
	}
	if !status.LoggedIn {
		data["login_url"] = s.absoluteURL("/auth/login")
	}
	writeAPIResponse(w, r, http.StatusOK, true, code, message, display, data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	status := s.session.Status()
	if status.LoggedIn && status.ExpiresAt.After(time.Now().Add(s.cfg.RefreshSkew)) {
		writeAPIResponse(w, r, http.StatusOK, true, "already_logged_in", "Already logged in", "当前已登录，无需重复登录。", map[string]any{
			"logged_in":  true,
			"user_id":    status.UserID,
			"user_name":  status.UserName,
			"scope":      status.Scope,
			"expires_at": status.ExpiresAt,
			"status_url": s.absoluteURL("/auth/status"),
		})
		return
	}
	if status.PendingLogin {
		writeAPIResponse(w, r, http.StatusAccepted, true, "login_in_progress", "Login already in progress", "登录流程已发起，请在浏览器完成登录。", map[string]any{
			"opened":        false,
			"auth_url":      status.AuthURL,
			"status_url":    s.absoluteURL("/auth/status"),
			"callback_path": s.cfg.CallbackPath,
			"next_step":     "完成登录后重新发起 MCP 请求",
		})
		return
	}
	login, err := s.session.EnsureLogin(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "login_start_failed", "Failed to start login flow", "无法自动打开浏览器，请复制登录链接手动完成登录。", map[string]any{
			"login_url": s.absoluteURL("/auth/login"),
		})
		return
	}
	writeAPIResponse(w, r, http.StatusAccepted, true, "login_started", "Login flow started", "请在浏览器完成登录，然后回到智能体重试。", map[string]any{
		"opened":        login.Opened,
		"auth_url":      login.AuthURL,
		"status_url":    s.absoluteURL("/auth/status"),
		"callback_path": s.cfg.CallbackPath,
		"expires_at":    login.ExpiresAt,
		"next_step":     "完成登录后重新发起 MCP 请求",
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if err := s.session.Logout(); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "logout_failed", "Failed to clear local login state", "退出登录失败，请稍后重试。", nil)
		return
	}
	writeAPIResponse(w, r, http.StatusOK, true, "logout_success", "Logged out", "已退出登录。", map[string]any{
		"logged_in": false,
	})
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r, http.MethodGet)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeHTML(w, http.StatusBadRequest, callbackPage("登录失败", "缺少 code 或 state 参数，请返回智能体重新发起登录。"))
		return
	}
	_, err := s.session.HandleCallback(r.Context(), code, state)
	if err != nil {
		writeHTML(w, http.StatusBadRequest, callbackPage("登录失败", callbackErrorMessage(err)))
		return
	}
	writeHTML(w, http.StatusOK, callbackPage("登录成功", "认证已完成，请回到智能体重新发起请求。"))
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Failed to read request body", "请求体读取失败，请重试。", nil)
		return
	}
	defer r.Body.Close()

	token, login, err := s.session.EnsureToken(r.Context())
	if err != nil {
		if errors.Is(err, session.ErrLoginRequired) {
			writeAPIError(w, r, http.StatusUnauthorized, "auth_required", "Authentication required", "访问企业 MCP 需要先登录，请完成浏览器登录后重试。", map[string]any{
				"login_url":     s.absoluteURL("/auth/login"),
				"status_url":    s.absoluteURL("/auth/status"),
				"auth_url":      login.AuthURL,
				"opened":        login.Opened,
				"retryable":     true,
				"next_step":     "完成登录后重新发起当前请求",
				"callback_path": s.cfg.CallbackPath,
			})
			return
		}
		writeAPIError(w, r, http.StatusBadGateway, "proxy_failed", "Failed to prepare access token", "代理处理登录状态失败，请稍后重试。", map[string]any{
			"retryable": true,
		})
		return
	}
	if err := s.forwardMCP(r.Context(), w, r, body, token.AccessToken, true); err != nil {
		log.Printf("forward mcp: %v", err)
		if !wroteHeader(w) {
			writeAPIError(w, r, http.StatusBadGateway, "upstream_unavailable", "Failed to reach upstream MCP gateway", "MCP 网关暂时不可用，请稍后重试。", map[string]any{
				"retryable": true,
			})
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
				writeAPIError(w, r, http.StatusUnauthorized, "token_expired", "Access token expired", "登录状态已过期，请重新登录后重试。", map[string]any{
					"login_url":  s.absoluteURL("/auth/login"),
					"status_url": s.absoluteURL("/auth/status"),
					"auth_url":   login.AuthURL,
					"opened":     login.Opened,
					"retryable":  true,
					"next_step":  "完成登录后重新发起当前请求",
				})
				return nil
			}
			return fmt.Errorf("refresh after token_expired: %w", err)
		}
		if upstream := parseUpstreamError(payload); upstream != nil {
			writeAPIError(w, r, http.StatusUnauthorized, upstreamCode(upstream.Code), upstreamMessage(upstream), displayMessage(upstream.Code), map[string]any{
				"login_url":  s.absoluteURL("/auth/login"),
				"status_url": s.absoluteURL("/auth/status"),
				"retryable":  true,
			})
			return nil
		}
		copyResponse(w, resp.Header, resp.StatusCode, bytes.NewReader(payload))
		return nil
	}
	copyResponse(w, resp.Header, resp.StatusCode, resp.Body)
	return nil
}

func isTokenExpired(body []byte) bool {
	upstream := parseUpstreamError(body)
	return upstream != nil && upstream.Code == "token_expired"
}

func parseUpstreamError(body []byte) *upstreamError {
	var payload upstreamError
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if payload.Code == "" {
		return nil
	}
	return &payload
}

func upstreamCode(code string) string {
	switch code {
	case "auth_required", "invalid_token", "token_expired", "auth_service_unavailable":
		return code
	default:
		return "proxy_failed"
	}
}

func upstreamMessage(upstream *upstreamError) string {
	if upstream.Message != "" {
		return upstream.Message
	}
	return strings.ReplaceAll(upstream.Code, "_", " ")
}

func displayMessage(code string) string {
	switch code {
	case "ok":
		return "操作成功"
	case "auth_required":
		return "访问企业 MCP 需要先登录，请完成浏览器登录后重试。"
	case "login_in_progress":
		return "检测到登录流程正在进行，请在浏览器完成登录后重试。"
	case "token_expired":
		return "登录状态已过期，请重新登录后重试。"
	case "invalid_token":
		return "当前登录凭证无效，请重新登录后重试。"
	case "auth_service_unavailable":
		return "登录服务暂时不可用，请稍后再试。"
	case "upstream_unavailable":
		return "MCP 网关暂时不可用，请稍后重试。"
	case "logout_success":
		return "已退出登录。"
	case "already_logged_in":
		return "当前已登录，无需重复登录。"
	case "login_started":
		return "请在浏览器完成登录，然后回到智能体重试。"
	case "login_start_failed":
		return "无法自动打开浏览器，请复制登录链接手动完成登录。"
	case "logout_failed":
		return "退出登录失败，请稍后重试。"
	case "invalid_request":
		return "请求参数不合法，请检查后重试。"
	case "method_not_allowed":
		return "当前接口不支持该请求方法。"
	default:
		return "请求处理失败，请稍后重试。"
	}
}

func callbackErrorMessage(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "no pending login session"):
		return "登录会话不存在或已过期，请返回智能体重新发起登录。"
	case strings.Contains(message, "state mismatch"):
		return "登录状态校验失败，请返回智能体重新发起登录。"
	case strings.Contains(message, "write token file"):
		return "Token 保存失败，请检查本地目录权限后重试。"
	default:
		return "登录未完成，请返回智能体重新发起登录。"
	}
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

func methodNotAllowed(w http.ResponseWriter, r *http.Request, method string) {
	w.Header().Set("Allow", method)
	writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", displayMessage("method_not_allowed"), nil)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIResponse(w http.ResponseWriter, r *http.Request, status int, success bool, code, message, display string, data map[string]any) {
	traceID := traceID(r)
	w.Header().Set("X-Trace-Id", traceID)
	if data == nil {
		data = map[string]any{}
	}
	writeJSON(w, status, apiResponse{
		Success:        success,
		Code:           code,
		Message:        message,
		DisplayMessage: display,
		TraceID:        traceID,
		Timestamp:      time.Now(),
		Data:           data,
	})
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message, display string, data map[string]any) {
	if display == "" {
		display = displayMessage(code)
	}
	writeAPIResponse(w, r, status, false, code, message, display, data)
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

func traceID(r *http.Request) string {
	if trace := strings.TrimSpace(r.Header.Get("X-Trace-Id")); trace != "" {
		return trace
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (s *Server) absoluteURL(path string) string {
	return "http://" + s.cfg.ListenAddr + path
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
