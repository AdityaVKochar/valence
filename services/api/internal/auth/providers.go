package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

var ErrDomainNotAllowed = errors.New("auth: email domain not allowed")

type Identity struct {
	Provider  string
	Subject   string
	Login     string
	Name      string
	Email     string
	AvatarURL string
}

type Provider interface {
	ID() string
	DisplayName() string
	AuthCodeURL(ctx context.Context, state, verifier string) (string, error)
	Exchange(ctx context.Context, code, verifier string) (Identity, error)
}

type GitHub struct {
	OAuth  oauth2.Config
	APIURL string
	Client *http.Client
}

func NewGitHub(clientID, secret, redirectURL string) *GitHub {
	return &GitHub{
		OAuth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: secret,
			Endpoint:     github.Endpoint,
			RedirectURL:  redirectURL,
			Scopes:       []string{"read:user", "user:email"},
		},
		APIURL: "https://api.github.com",
	}
}

func (g *GitHub) ID() string          { return "github" }
func (g *GitHub) DisplayName() string { return "GitHub" }

func (g *GitHub) AuthCodeURL(_ context.Context, state, verifier string) (string, error) {
	return g.OAuth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), nil
}

func (g *GitHub) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	if g.Client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, g.Client)
	}
	tok, err := g.OAuth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("github: exchange code: %w", err)
	}
	client := g.OAuth.Client(ctx, tok)
	var u struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getJSON(ctx, client, g.APIURL+"/user", &u); err != nil {
		return Identity{}, fmt.Errorf("github: fetch user: %w", err)
	}
	if u.ID == 0 {
		return Identity{}, errors.New("github: user has no id")
	}
	if u.Email == "" {
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := getJSON(ctx, client, g.APIURL+"/user/emails", &emails); err == nil {
			for _, e := range emails {
				if e.Primary && e.Verified {
					u.Email = e.Email
				}
			}
		}
	}
	return Identity{
		Provider:  "github",
		Subject:   strconv.FormatInt(u.ID, 10),
		Login:     u.Login,
		Name:      u.Name,
		Email:     u.Email,
		AvatarURL: u.AvatarURL,
	}, nil
}

func getJSON(ctx context.Context, c *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type Google struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AllowedDomains []string
	Issuer         string

	mu       sync.Mutex
	provider *oidc.Provider
}

func NewGoogle(clientID, secret, redirectURL string, allowedDomains []string) *Google {
	return &Google{
		ClientID:       clientID,
		ClientSecret:   secret,
		RedirectURL:    redirectURL,
		AllowedDomains: allowedDomains,
		Issuer:         "https://accounts.google.com",
	}
}

func (g *Google) ID() string          { return "google" }
func (g *Google) DisplayName() string { return "Google" }

func (g *Google) init(ctx context.Context) (*oauth2.Config, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider == nil {
		p, err := oidc.NewProvider(context.WithoutCancel(ctx), g.Issuer)
		if err != nil {
			return nil, fmt.Errorf("google: discover issuer: %w", err)
		}
		g.provider = p
	}
	return &oauth2.Config{
		ClientID:     g.ClientID,
		ClientSecret: g.ClientSecret,
		Endpoint:     g.provider.Endpoint(),
		RedirectURL:  g.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}, nil
}

func (g *Google) AuthCodeURL(ctx context.Context, state, verifier string) (string, error) {
	cfg, err := g.init(ctx)
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", "select_account")}
	if len(g.AllowedDomains) == 1 {
		opts = append(opts, oauth2.SetAuthURLParam("hd", g.AllowedDomains[0]))
	}
	return cfg.AuthCodeURL(state, opts...), nil
}

func (g *Google) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	cfg, err := g.init(ctx)
	if err != nil {
		return Identity{}, err
	}
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("google: exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("google: no id_token in response")
	}
	g.mu.Lock()
	provider := g.provider
	g.mu.Unlock()
	idt, err := provider.Verifier(&oidc.Config{ClientID: g.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("google: verify id_token: %w", err)
	}
	var c GoogleClaims
	if err := idt.Claims(&c); err != nil {
		return Identity{}, err
	}
	c.Subject = idt.Subject
	return c.Identity(g.AllowedDomains)
}

type GoogleClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

func (c GoogleClaims) Identity(allowedDomains []string) (Identity, error) {
	email := strings.ToLower(c.Email)
	_, domain, ok := strings.Cut(email, "@")
	if !ok || !c.EmailVerified || !slices.Contains(allowedDomains, domain) {
		return Identity{}, ErrDomainNotAllowed
	}
	login, _, _ := strings.Cut(email, "@")
	return Identity{
		Provider:  "google",
		Subject:   c.Subject,
		Login:     login,
		Name:      c.Name,
		Email:     email,
		AvatarURL: c.Picture,
	}, nil
}
