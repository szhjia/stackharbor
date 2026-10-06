package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"github.com/szhjia/stackharbor/internal/control"
	"net/http"
	"sync"
	"time"
)

const cookiePrefix = "stackharbor_session_"
const sessionIdle = 8 * time.Hour
const credentialTTL = 60 * time.Second

// Only hashes of bearer credentials are retained. All credentials die on restart.
type browserSession struct {
	CSRF     string
	LastSeen time.Time
}
type authStore struct {
	cookieName string
	mu         sync.Mutex
	tokens     map[[32]byte]time.Time
	sessions   map[[32]byte]browserSession
	now        func() time.Time
}

func newAuth(cookieName string) *authStore {
	return &authStore{cookieName: cookieName, tokens: map[[32]byte]time.Time{}, sessions: map[[32]byte]browserSession{}, now: time.Now}
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (a *authStore) prune(now time.Time) {
	for k, v := range a.tokens {
		if !now.Before(v) {
			delete(a.tokens, k)
		}
	}
	for k, v := range a.sessions {
		if !now.Before(v.LastSeen.Add(sessionIdle)) {
			delete(a.sessions, k)
		}
	}
}
func (a *authStore) issue() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	if len(a.tokens) >= 128 {
		return "", apiError("queue_full", "too many pending browser credentials; retry after 60 seconds")
	}
	token, err := randomToken()
	if err == nil {
		a.tokens[sha256.Sum256([]byte(token))] = now.Add(credentialTTL)
	}
	return token, err
}
func (a *authStore) exchange(token string, priorCookies ...string) (string, browserSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	key := sha256.Sum256([]byte(token))
	if _, ok := a.tokens[key]; !ok {
		return "", browserSession{}, apiError("unauthenticated", "credential expired or already used; run stackharbor web to authenticate again")
	}
	var priorKey [32]byte
	replacing := false
	if len(priorCookies) > 0 && priorCookies[0] != "" {
		priorKey = sha256.Sum256([]byte(priorCookies[0]))
		_, replacing = a.sessions[priorKey]
	}
	if len(a.sessions) >= 32 && !replacing {
		return "", browserSession{}, apiError("queue_full", "browser session capacity reached; retry after an idle session expires")
	}
	cookie, err := randomToken()
	if err != nil {
		return "", browserSession{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", browserSession{}, err
	}
	session := browserSession{csrf, now}
	// Rotate atomically only after all validation and credential generation succeeds.
	delete(a.tokens, key)
	if replacing {
		delete(a.sessions, priorKey)
	}
	a.sessions[sha256.Sum256([]byte(cookie))] = session
	return cookie, session, nil
}
func (a *authStore) authenticate(r *http.Request, write bool) (browserSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	c, err := r.Cookie(a.cookieName)
	if err != nil {
		return browserSession{}, apiError("unauthenticated", "run stackharbor web to authenticate this browser")
	}
	key := sha256.Sum256([]byte(c.Value))
	session, ok := a.sessions[key]
	if !ok {
		return browserSession{}, apiError("unauthenticated", "browser session expired; run stackharbor web again")
	}
	if write && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRF)) != 1 {
		return browserSession{}, apiError("forbidden", "valid CSRF token required")
	}
	session.LastSeen = now
	a.sessions[key] = session
	return session, nil
}
func apiError(code, message string) *control.APIError {
	return &control.APIError{Code: code, Message: message}
}
