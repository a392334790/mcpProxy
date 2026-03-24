package model

import "time"

type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       string    `json:"user_id,omitempty"`
	UserName     string    `json:"user_name,omitempty"`
}

func (t *TokenSet) LoggedIn() bool {
	return t != nil && t.AccessToken != ""
}

func (t *TokenSet) NeedsRefresh(now time.Time, skew time.Duration) bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !t.ExpiresAt.After(now.Add(skew))
}
