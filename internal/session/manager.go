package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"mcpProxy/internal/config"
	"mcpProxy/internal/model"
	"mcpProxy/internal/oauth"
	"mcpProxy/internal/storage"
)

var ErrLoginRequired = errors.New("login required")

type Manager struct {
	cfg        *config.Config
	store      storage.TokenStore
	httpClient *http.Client

	mu           sync.Mutex
	token        *model.TokenSet
	pendingLogin *LoginSession
	refreshing   bool
	refreshWait  chan struct{}
	openBrowser  func(string) error
	now          func() time.Time
}

type LoginSession struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"-"`
	AuthURL      string    `json:"auth_url"`
	StartedAt    time.Time `json:"started_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Opened       bool      `json:"opened"`
}

type Status struct {
	LoggedIn     bool      `json:"logged_in"`
	UserID       string    `json:"user_id,omitempty"`
	UserName     string    `json:"user_name,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	PendingLogin bool      `json:"pending_login"`
	AuthURL      string    `json:"auth_url,omitempty"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	Exp          int64  `json:"exp"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func NewManager(cfg *config.Config, store storage.TokenStore) (*Manager, error) {
	manager := &Manager{
		cfg:   cfg,
		store: store,
		httpClient: &http.Client{
			Timeout: cfg.TokenTimeout,
		},
		openBrowser: openBrowser,
		now:         time.Now,
	}
	token, err := store.Load()
	if err != nil {
		return nil, err
	}
	manager.token = token
	return manager, nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{}
	if m.token != nil && m.token.LoggedIn() {
		status.LoggedIn = true
		status.UserID = m.token.UserID
		status.UserName = m.token.UserName
		status.ExpiresAt = m.token.ExpiresAt
	}
	if m.pendingLogin != nil && m.pendingLogin.ExpiresAt.After(m.now()) {
		status.PendingLogin = true
		status.AuthURL = m.pendingLogin.AuthURL
	}
	return status
}

func (m *Manager) Logout() error {
	m.mu.Lock()
	m.token = nil
	m.pendingLogin = nil
	m.mu.Unlock()
	return m.store.Delete()
}

func (m *Manager) EnsureToken(ctx context.Context) (*model.TokenSet, *LoginSession, error) {
	for {
		m.mu.Lock()
		if m.token != nil && !m.token.NeedsRefresh(m.now(), m.cfg.RefreshSkew) {
			copyToken := *m.token
			m.mu.Unlock()
			return &copyToken, nil, nil
		}
		if m.token != nil && m.token.RefreshToken != "" {
			m.mu.Unlock()
			return m.Refresh(ctx)
		}
		m.mu.Unlock()

		login, err := m.EnsureLogin(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, login, ErrLoginRequired
	}
}

func (m *Manager) Refresh(ctx context.Context) (*model.TokenSet, *LoginSession, error) {
	for {
		m.mu.Lock()
		if m.token == nil || m.token.RefreshToken == "" {
			m.mu.Unlock()
			login, err := m.EnsureLogin(ctx)
			if err != nil {
				return nil, nil, err
			}
			return nil, login, ErrLoginRequired
		}
		if m.refreshing {
			wait := m.refreshWait
			m.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		m.refreshing = true
		m.refreshWait = make(chan struct{})
		refreshToken := m.token.RefreshToken
		m.mu.Unlock()

		refreshed, err := m.refreshToken(ctx, refreshToken)

		m.mu.Lock()
		wait := m.refreshWait
		m.refreshing = false
		m.refreshWait = nil
		if err == nil {
			m.token = refreshed
			m.pendingLogin = nil
		}
		m.mu.Unlock()
		close(wait)

		if err == nil {
			return refreshed, nil, nil
		}
		log.Printf("refresh token failed: %v", err)
		if isOAuthInvalidGrant(err) {
			if clearErr := m.clearToken(); clearErr != nil {
				log.Printf("clear invalid token: %v", clearErr)
			}
			login, loginErr := m.EnsureLogin(ctx)
			if loginErr != nil {
				return nil, nil, loginErr
			}
			return nil, login, ErrLoginRequired
		}
		return nil, nil, err
	}
}

func (m *Manager) EnsureLogin(_ context.Context) (*LoginSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.pendingLogin != nil && m.pendingLogin.ExpiresAt.After(now) {
		copyLogin := *m.pendingLogin
		return &copyLogin, nil
	}
	state, err := oauth.RandomState()
	if err != nil {
		return nil, err
	}
	verifier, err := oauth.GenerateVerifier()
	if err != nil {
		return nil, err
	}
	challenge := oauth.ChallengeS256(verifier)
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", m.cfg.ClientID)
	values.Set("redirect_uri", m.cfg.RedirectURL)
	values.Set("scope", m.cfg.Scope)
	values.Set("code_challenge", challenge)
	values.Set("code_challenge_method", "S256")
	values.Set("state", state)
	authURL := m.cfg.AuthorizeURL
	separator := "?"
	if bytes.Contains([]byte(authURL), []byte("?")) {
		separator = "&"
	}
	authURL = authURL + separator + values.Encode()
	login := &LoginSession{
		State:        state,
		CodeVerifier: verifier,
		AuthURL:      authURL,
		StartedAt:    now,
		ExpiresAt:    now.Add(m.cfg.LoginStateTTL),
	}
	m.pendingLogin = login
	if m.cfg.AutoOpenBrowser {
		if err := m.openBrowser(authURL); err != nil {
			m.pendingLogin = nil
			return nil, fmt.Errorf("open browser: %w", err)
		}
		login.Opened = true
	}
	copyLogin := *login
	return &copyLogin, nil
}

func (m *Manager) HandleCallback(ctx context.Context, code, state string) (*model.TokenSet, error) {
	m.mu.Lock()
	pending := m.pendingLogin
	m.mu.Unlock()

	if pending == nil || !pending.ExpiresAt.After(m.now()) {
		return nil, errors.New("no pending login session")
	}
	if pending.State != state {
		return nil, errors.New("state mismatch")
	}
	token, err := m.exchangeCode(ctx, code, pending.CodeVerifier)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.token = token
	m.pendingLogin = nil
	m.mu.Unlock()
	if err := m.store.Save(token); err != nil {
		return nil, err
	}
	return token, nil
}

func (m *Manager) exchangeCode(ctx context.Context, code, verifier string) (*model.TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", m.cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", m.cfg.RedirectURL)
	form.Set("code_verifier", verifier)
	return m.requestToken(ctx, form)
}

func (m *Manager) refreshToken(ctx context.Context, refreshToken string) (*model.TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", m.cfg.ClientID)
	form.Set("refresh_token", refreshToken)
	return m.requestToken(ctx, form)
}

func (m *Manager) requestToken(ctx context.Context, form url.Values) (*model.TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.TokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	var payload oauthTokenResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if payload.Error == "" {
			payload.Error = fmt.Sprintf("token_endpoint_%d", resp.StatusCode)
		}
		return nil, oauthError{Code: payload.Error, Description: payload.Description}
	}
	if payload.AccessToken == "" {
		return nil, errors.New("token endpoint returned empty access_token")
	}
	expiresAt := m.now().Add(time.Hour)
	if payload.ExpiresIn > 0 {
		expiresAt = m.now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	} else if payload.Exp > 0 {
		expiresAt = time.Unix(payload.Exp, 0)
	}
	token := &model.TokenSet{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		TokenType:    payload.TokenType,
		Scope:        payload.Scope,
		ExpiresAt:    expiresAt,
		UserID:       payload.UserID,
		UserName:     payload.UserName,
	}
	if token.RefreshToken == "" {
		m.mu.Lock()
		if m.token != nil {
			token.RefreshToken = m.token.RefreshToken
		}
		m.mu.Unlock()
	}
	if err := m.store.Save(token); err != nil {
		return nil, err
	}
	return token, nil
}

func (m *Manager) clearToken() error {
	m.mu.Lock()
	m.token = nil
	m.mu.Unlock()
	return m.store.Delete()
}

type oauthError struct {
	Code        string
	Description string
}

func (e oauthError) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

func isOAuthInvalidGrant(err error) bool {
	var oauthErr oauthError
	if !errors.As(err, &oauthErr) {
		return false
	}
	return oauthErr.Code == "invalid_grant" || oauthErr.Code == "invalid_token"
}
