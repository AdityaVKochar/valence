package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/session"
)

const (
	CookieName      = "valence_session"
	oauthCookieName = "valence_oauth"
	oauthCookieTTL  = 10 * time.Minute
)

type Options struct {
	Pool      *pgxpool.Pool
	Sessions  session.Store
	TTL       time.Duration
	WebOrigin string
	Secure    bool
	Dev       bool
	Providers []Provider
	Logger    *slog.Logger
	Now       func() time.Time
}

type Service struct {
	opts Options
	q    *db.Queries
}

func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, q: db.New(opts.Pool)}
}

type ProviderInfo struct {
	ID          string
	DisplayName string
	LoginURL    string
}

func (s *Service) Providers() []ProviderInfo {
	var out []ProviderInfo
	for _, p := range s.opts.Providers {
		out = append(out, ProviderInfo{p.ID(), p.DisplayName(), "/auth/" + p.ID() + "/login"})
	}
	if s.opts.Dev {
		out = append(out, ProviderInfo{"dev", "Development login", "/auth/dev"})
	}
	return out
}

func (s *Service) Routes(mux *http.ServeMux) {
	for _, p := range s.opts.Providers {
		mux.HandleFunc("GET /auth/"+p.ID()+"/login", func(w http.ResponseWriter, r *http.Request) { s.login(w, r, p) })
		mux.HandleFunc("GET /auth/"+p.ID()+"/callback", func(w http.ResponseWriter, r *http.Request) { s.callback(w, r, p) })
	}
	mux.HandleFunc("POST /auth/logout", s.logout)
	if s.opts.Dev {
		mux.HandleFunc("GET /auth/dev", s.devLogin)
	}
}

func (s *Service) Interceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			var refresh string
			if token := cookieValue(req.Header(), CookieName); token != "" {
				u, newCookie, err := s.resolve(ctx, token)
				if err != nil {
					return nil, err
				}
				if u != nil {
					ctx = WithUser(ctx, u)
				}
				refresh = newCookie
			}
			resp, err := next(ctx, req)
			if refresh != "" && resp != nil {
				resp.Header().Add("Set-Cookie", refresh)
			}
			return resp, err
		}
	})
}

func (s *Service) UserFromRequest(r *http.Request) (*db.User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, nil
	}
	u, _, err := s.resolve(r.Context(), c.Value)
	return u, err
}

func (s *Service) resolve(ctx context.Context, token string) (*db.User, string, error) {
	hash, ok := hashToken(token)
	if !ok {
		return nil, "", nil
	}
	sess, err := s.opts.Sessions.Get(ctx, hash)
	if errors.Is(err, session.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	u, err := s.q.GetUser(ctx, sess.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if u.DisabledAt != nil {
		return nil, "", nil
	}
	var refresh string
	now := s.opts.Now()
	if sess.ExpiresAt.Sub(now) < s.opts.TTL/2 {
		expires := now.Add(s.opts.TTL)
		if err := s.opts.Sessions.Touch(ctx, hash, expires); err != nil {
			return nil, "", err
		}
		refresh = s.sessionCookie(token, expires).String()
	}
	return &u, refresh, nil
}

func (s *Service) StartSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := hashToken(token)
	expires := s.opts.Now().Add(s.opts.TTL)
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	err := s.opts.Sessions.Create(ctx, session.Session{
		IDHash:    hash,
		UserID:    userID,
		ExpiresAt: expires,
		UserAgent: ua,
		IP:        clientIP(r),
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, s.sessionCookie(token, expires))
	return nil
}

func (s *Service) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   s.opts.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if hash, ok := hashToken(c.Value); ok {
			if err := s.opts.Sessions.Delete(r.Context(), hash); err != nil {
				s.opts.Logger.Error("Delete session", "err", err)
				http.Error(w, "Could not log out", http.StatusInternalServerError)
				return
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.opts.Secure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

type oauthState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
}

func (s *Service) login(w http.ResponseWriter, r *http.Request, p Provider) {
	st := oauthState{Provider: p.ID(), State: randomString(), Verifier: oauth2.GenerateVerifier(), Next: safeNext(r.URL.Query().Get("next"))}
	target, err := p.AuthCodeURL(r.Context(), st.State, st.Verifier)
	if err != nil {
		s.opts.Logger.Error("Start login", "provider", p.ID(), "err", err)
		s.redirectError(w, r, "provider_unavailable")
		return
	}
	b, _ := json.Marshal(st)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(b),
		Path:     "/auth/",
		MaxAge:   int(oauthCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.opts.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request, p Provider) {
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Value: "", Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: s.opts.Secure, SameSite: http.SameSiteLaxMode})
	var st oauthState
	c, err := r.Cookie(oauthCookieName)
	if err == nil {
		if b, derr := base64.RawURLEncoding.DecodeString(c.Value); derr == nil {
			err = json.Unmarshal(b, &st)
		} else {
			err = derr
		}
	}
	q := r.URL.Query()
	if err != nil || st.Provider != p.ID() || st.State == "" || subtle.ConstantTimeCompare([]byte(st.State), []byte(q.Get("state"))) != 1 {
		s.redirectError(w, r, "invalid_state")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		s.redirectError(w, r, "access_denied")
		return
	}
	id, err := p.Exchange(r.Context(), q.Get("code"), st.Verifier)
	if errors.Is(err, ErrDomainNotAllowed) {
		s.redirectError(w, r, "domain_not_allowed")
		return
	}
	if err != nil {
		s.opts.Logger.Error("OAuth exchange failed", "provider", p.ID(), "err", err)
		s.redirectError(w, r, "provider_error")
		return
	}
	s.finishLogin(w, r, id, nil, st.Next)
}

func (s *Service) devLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	handle := q.Get("user")
	if handle == "" {
		handle = "dev"
	}
	if !validHandle(handle) {
		http.Error(w, "user must be 3 to 32 characters of letters, digits, '_' and '-'", http.StatusBadRequest)
		return
	}
	var role *db.UserRole
	if v := q.Get("role"); v != "" {
		r := db.UserRole(v)
		if !r.Valid() {
			http.Error(w, "role must be contestant, setter or admin", http.StatusBadRequest)
			return
		}
		role = &r
	}
	s.finishLogin(w, r, Identity{Provider: "dev", Subject: strings.ToLower(handle), Login: handle, Name: handle}, role, safeNext(q.Get("next")))
}

func (s *Service) finishLogin(w http.ResponseWriter, r *http.Request, id Identity, role *db.UserRole, next string) {
	u, err := s.FindOrCreate(r.Context(), id)
	if err == nil && role != nil && u.Role != *role {
		u, err = s.q.SetUserRole(r.Context(), db.SetUserRoleParams{Role: *role, ID: u.ID})
	}
	if err != nil {
		s.opts.Logger.Error("Log in", "provider", id.Provider, "err", err)
		s.redirectError(w, r, "server_error")
		return
	}
	if u.DisabledAt != nil {
		s.redirectError(w, r, "account_disabled")
		return
	}
	if err := s.StartSession(r.Context(), w, r, u.ID); err != nil {
		s.opts.Logger.Error("Start session", "err", err)
		s.redirectError(w, r, "server_error")
		return
	}
	s.opts.Logger.Info("User logged in", "user_id", u.ID, "handle", u.Handle, "provider", id.Provider)
	if r.URL.Query().Get("redirect") == "0" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "handle": u.Handle, "role": u.Role})
		return
	}
	http.Redirect(w, r, s.opts.WebOrigin+next, http.StatusFound)
}

func (s *Service) FindOrCreate(ctx context.Context, id Identity) (db.User, error) {
	var user db.User
	attempt := func() error {
		return pg.InTx(ctx, s.opts.Pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			email := optional(id.Email)
			u, err := q.GetUserByIdentity(ctx, db.GetUserByIdentityParams{Provider: id.Provider, Subject: id.Subject})
			if err == nil {
				if err := q.TouchIdentity(ctx, db.TouchIdentityParams{Email: email, Provider: id.Provider, Subject: id.Subject}); err != nil {
					return err
				}
				user, err = q.RecordLogin(ctx, db.RecordLoginParams{AvatarUrl: optional(id.AvatarURL), Email: email, ID: u.ID})
				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			handle, err := uniqueHandle(ctx, id.Login, q.HandleTaken)
			if err != nil {
				return err
			}
			name := id.Name
			if len([]rune(name)) > 64 {
				name = string([]rune(name)[:64])
			}
			u, err = q.CreateUser(ctx, db.CreateUserParams{
				Handle:      handle,
				DisplayName: name,
				Email:       email,
				AvatarUrl:   optional(id.AvatarURL),
				Role:        db.UserRoleContestant,
			})
			if err != nil {
				return err
			}
			if err := q.CreateIdentity(ctx, db.CreateIdentityParams{Provider: id.Provider, Subject: id.Subject, UserID: u.ID, Email: email}); err != nil {
				return err
			}
			user, err = q.RecordLogin(ctx, db.RecordLoginParams{ID: u.ID})
			return err
		})
	}
	err := attempt()
	if pg.IsUniqueViolation(err) {
		err = attempt()
	}
	return user, err
}

func (s *Service) redirectError(w http.ResponseWriter, r *http.Request, code string) {
	if r.URL.Query().Get("redirect") == "0" {
		http.Error(w, code, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, s.opts.WebOrigin+"/login?error="+url.QueryEscape(code), http.StatusFound)
}

func hashToken(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

func cookieValue(h http.Header, name string) string {
	c, err := (&http.Request{Header: h}).Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

func validHandle(h string) bool {
	return len(h) >= 3 && len(h) <= 32 && !invalidHandleChars.MatchString(h)
}

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
