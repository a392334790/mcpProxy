package session

import (
	"context"
	"testing"
	"time"

	"mcpProxy/internal/config"
	"mcpProxy/internal/model"
)

type memoryTokenStore struct {
	token *model.TokenSet
}

func (s *memoryTokenStore) Load() (*model.TokenSet, error)   { return s.token, nil }
func (s *memoryTokenStore) Save(token *model.TokenSet) error { s.token = token; return nil }
func (s *memoryTokenStore) Delete() error                    { s.token = nil; return nil }

func TestEnsureLoginStoresPendingBeforeOpeningBrowser(t *testing.T) {
	store := &memoryTokenStore{}
	mgr, err := NewManager(&config.Config{
		AuthorizeURL:    "http://127.0.0.1:18080/oauth2/authorize",
		ClientID:        "local-mcp-proxy",
		RedirectURL:     "http://127.0.0.1:8765/auth/callback",
		Scope:           "mcp.invoke mcp.read",
		LoginStateTTL:   time.Minute,
		AutoOpenBrowser: true,
	}, store)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}

	called := false
	mgr.openBrowser = func(_ string) error {
		called = true
		if mgr.pendingLogin == nil {
			t.Fatalf("pendingLogin should be set before openBrowser")
		}
		return nil
	}

	login, err := mgr.EnsureLogin(context.Background())
	if err != nil {
		t.Fatalf("EnsureLogin error: %v", err)
	}
	if !called {
		t.Fatalf("openBrowser was not called")
	}
	if login == nil || !login.Opened {
		t.Fatalf("expected opened login session")
	}
}

func TestHandleCallbackFailureClearsPendingLogin(t *testing.T) {
	store := &memoryTokenStore{}
	mgr, err := NewManager(&config.Config{
		AuthorizeURL:    "http://127.0.0.1:18080/oauth2/authorize",
		ClientID:        "local-mcp-proxy",
		RedirectURL:     "http://127.0.0.1:8765/auth/callback",
		Scope:           "mcp.invoke mcp.read",
		LoginStateTTL:   time.Minute,
		AutoOpenBrowser: false,
	}, store)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}

	first, err := mgr.EnsureLogin(context.Background())
	if err != nil {
		t.Fatalf("EnsureLogin error: %v", err)
	}
	if first == nil || first.State == "" {
		t.Fatalf("expected initial login session")
	}

	if _, err := mgr.HandleCallback(context.Background(), "dummy-code", "wrong-state"); err == nil {
		t.Fatalf("expected state mismatch error")
	}
	if mgr.pendingLogin != nil {
		t.Fatalf("pendingLogin should be cleared after callback failure")
	}

	second, err := mgr.EnsureLogin(context.Background())
	if err != nil {
		t.Fatalf("EnsureLogin retry error: %v", err)
	}
	if second == nil || second.State == "" {
		t.Fatalf("expected new login session")
	}
	if second.State == first.State {
		t.Fatalf("expected a fresh login state after callback failure")
	}
}
