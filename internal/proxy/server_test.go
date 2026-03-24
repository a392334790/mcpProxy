package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mcpProxy/internal/config"
	"mcpProxy/internal/model"
	"mcpProxy/internal/session"
)

type memoryTokenStore struct {
	token *model.TokenSet
}

func (s *memoryTokenStore) Load() (*model.TokenSet, error)   { return s.token, nil }
func (s *memoryTokenStore) Save(token *model.TokenSet) error { s.token = token; return nil }
func (s *memoryTokenStore) Delete() error                    { s.token = nil; return nil }

func newTestServer(t *testing.T, token *model.TokenSet) http.Handler {
	t.Helper()
	manager, err := session.NewManager(&config.Config{
		ListenAddr:      "127.0.0.1:8765",
		CallbackPath:    "/auth/callback",
		RedirectURL:     "http://127.0.0.1:8765/auth/callback",
		UpstreamMCPURL:  "http://127.0.0.1:19000/mcp",
		AuthorizeURL:    "http://127.0.0.1:18080/oauth2/authorize",
		TokenURL:        "http://127.0.0.1:18080/oauth2/token",
		ClientID:        "local-mcp-proxy",
		Scope:           "mcp.invoke mcp.read",
		TokenFile:       "runtime/test-token.dat",
		AutoOpenBrowser: false,
		RefreshSkew:     time.Minute,
		LoginStateTTL:   10 * time.Minute,
		TokenTimeout:    5 * time.Second,
	}, &memoryTokenStore{token: token})
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	return NewServer(&config.Config{
		ListenAddr:      "127.0.0.1:8765",
		CallbackPath:    "/auth/callback",
		RedirectURL:     "http://127.0.0.1:8765/auth/callback",
		UpstreamMCPURL:  "http://127.0.0.1:19000/mcp",
		AuthorizeURL:    "http://127.0.0.1:18080/oauth2/authorize",
		TokenURL:        "http://127.0.0.1:18080/oauth2/token",
		ClientID:        "local-mcp-proxy",
		Scope:           "mcp.invoke mcp.read",
		TokenFile:       "runtime/test-token.dat",
		AutoOpenBrowser: false,
		RefreshSkew:     time.Minute,
		LoginStateTTL:   10 * time.Minute,
		TokenTimeout:    5 * time.Second,
	}, manager)
}

func decodeAPIResponse(t *testing.T, rec *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var payload apiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestHandleStatusUsesEnvelope(t *testing.T) {
	handler := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d", rec.Code)
	}
	payload := decodeAPIResponse(t, rec)
	if !payload.Success || payload.Code != "ok" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Data["login_url"] == nil {
		t.Fatalf("expected login_url in data")
	}
	if rec.Header().Get("X-Trace-Id") == "" {
		t.Fatalf("expected X-Trace-Id header")
	}
}

func TestHandleLoginReturnsAlreadyLoggedIn(t *testing.T) {
	handler := newTestServer(t, &model.TokenSet{
		AccessToken: "token",
		Scope:       "mcp.invoke mcp.read",
		UserID:      "u12345",
		UserName:    "zhangsan",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d", rec.Code)
	}
	payload := decodeAPIResponse(t, rec)
	if payload.Code != "already_logged_in" {
		t.Fatalf("unexpected code: %s", payload.Code)
	}
	if payload.DisplayMessage == "" {
		t.Fatalf("expected display message")
	}
}

func TestHandleMCPAuthRequiredUsesEnvelope(t *testing.T) {
	handler := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d", rec.Code)
	}
	payload := decodeAPIResponse(t, rec)
	if payload.Success {
		t.Fatalf("expected failure payload")
	}
	if payload.Code != "auth_required" {
		t.Fatalf("unexpected code: %s", payload.Code)
	}
	if payload.Data["login_url"] == nil || payload.Data["status_url"] == nil {
		t.Fatalf("expected login and status urls in data")
	}
}
