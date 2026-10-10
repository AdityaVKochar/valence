package config

import (
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Env string

const (
	Dev  Env = "dev"
	Test Env = "test"
	Prod Env = "prod"
)

const devInternalToken = "dev-internal-token"

type Common struct {
	Env           Env    `env:"VALENCE_ENV" envDefault:"dev"`
	DatabaseURL   string `env:"DATABASE_URL,required,notEmpty"`
	InternalToken string `env:"INTERNAL_TOKEN"`
	LogLevel      string `env:"LOG_LEVEL" envDefault:"info"`
}

type API struct {
	Common
	Addr                 string        `env:"API_ADDR" envDefault:":8080"`
	InternalAddr         string        `env:"API_INTERNAL_ADDR" envDefault:":8090"`
	WebOrigin            string        `env:"WEB_ORIGIN" envDefault:"http://localhost:5173"`
	PublicURL            string        `env:"PUBLIC_API_URL" envDefault:"http://localhost:8080"`
	BlobDir              string        `env:"BLOB_DIR" envDefault:".data/blobs"`
	GitHubClientID       string        `env:"GITHUB_CLIENT_ID"`
	GitHubClientSecret   string        `env:"GITHUB_CLIENT_SECRET"`
	GoogleClientID       string        `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret   string        `env:"GOOGLE_CLIENT_SECRET"`
	GoogleAllowedDomains []string      `env:"GOOGLE_ALLOWED_DOMAINS" envSeparator:","`
	SessionTTL           time.Duration `env:"SESSION_TTL" envDefault:"720h"`
	MaxCodeKiB           int           `env:"MAX_CODE_KIB" envDefault:"64"`
	SubmitCooldown       time.Duration `env:"SUBMIT_COOLDOWN" envDefault:"5s"`
	MaxActiveSubmissions int           `env:"MAX_ACTIVE_SUBMISSIONS" envDefault:"3"`
	AutoMigrate          *bool         `env:"AUTO_MIGRATE"`
}

type Worker struct {
	Common
	Addr           string `env:"WORKER_ADDR" envDefault:":8081"`
	APIInternalURL string `env:"API_INTERNAL_URL" envDefault:"http://localhost:8090"`
	Slots          int    `env:"WORKER_SLOTS" envDefault:"0"`
	Provider       string `env:"JUDGE_PROVIDER" envDefault:"auto"`
	CacheDir       string `env:"JUDGE_CACHE_DIR" envDefault:".data/judge-cache"`
	CacheMaxMiB    int64  `env:"JUDGE_CACHE_MAX_MIB" envDefault:"2048"`
	WorkDir        string `env:"JUDGE_WORK_DIR" envDefault:".data/judge-work"`
	IsolateBin     string `env:"ISOLATE_BIN" envDefault:"isolate"`
	IsolateBoxBase int    `env:"ISOLATE_BOX_BASE" envDefault:"0"`
}

func LoadAPI() (API, error) {
	return load(func(c *API) []error {
		errs := c.Common.validate()
		errs = append(errs, checkURL("WEB_ORIGIN", c.WebOrigin), checkURL("PUBLIC_API_URL", c.PublicURL))
		if c.MaxCodeKiB < 1 || c.MaxCodeKiB > 256 {
			errs = append(errs, errors.New("MAX_CODE_KIB: must be between 1 and 256"))
		}
		if c.SessionTTL < time.Hour {
			errs = append(errs, errors.New("SESSION_TTL: must be at least 1h"))
		}
		if c.SubmitCooldown < 0 {
			errs = append(errs, errors.New("SUBMIT_COOLDOWN: must not be negative"))
		}
		if c.MaxActiveSubmissions < 0 {
			errs = append(errs, errors.New("MAX_ACTIVE_SUBMISSIONS: must not be negative"))
		}
		if (c.GitHubClientID == "") != (c.GitHubClientSecret == "") {
			errs = append(errs, errors.New("GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET: set both or neither"))
		}
		if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
			errs = append(errs, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET: set both or neither"))
		}
		if c.GoogleClientID != "" && len(c.GoogleAllowedDomains) == 0 {
			errs = append(errs, errors.New("GOOGLE_ALLOWED_DOMAINS: required when Google login is enabled"))
		}
		for i, d := range c.GoogleAllowedDomains {
			c.GoogleAllowedDomains[i] = strings.ToLower(strings.TrimSpace(d))
		}
		return errs
	})
}

func LoadWorker() (Worker, error) {
	return load(func(c *Worker) []error {
		errs := c.Common.validate()
		errs = append(errs, checkURL("API_INTERNAL_URL", c.APIInternalURL))
		if c.Slots < 0 {
			errs = append(errs, errors.New("WORKER_SLOTS: must not be negative"))
		}
		if c.Slots == 0 {
			c.Slots = max(runtime.NumCPU()-1, 1)
		}
		switch c.Provider {
		case "auto", "local-isolate", "local-unsafe":
		default:
			errs = append(errs, fmt.Errorf("JUDGE_PROVIDER: %q is not one of auto, local-isolate, local-unsafe", c.Provider))
		}
		if c.CacheMaxMiB < 1 {
			errs = append(errs, errors.New("JUDGE_CACHE_MAX_MIB: must be positive"))
		}
		return errs
	})
}

func (c API) AutoMigrateEnabled() bool {
	if c.AutoMigrate != nil {
		return *c.AutoMigrate
	}
	return c.Env == Dev
}

func (c *Common) validate() []error {
	var errs []error
	switch c.Env {
	case Dev, Test, Prod:
	default:
		errs = append(errs, fmt.Errorf("VALENCE_ENV: %q is not one of dev, test, prod", c.Env))
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %q is not one of debug, info, warn, error", c.LogLevel))
	}
	if c.InternalToken == "" {
		if c.Env == Prod {
			errs = append(errs, errors.New("INTERNAL_TOKEN: required when VALENCE_ENV=prod"))
		} else {
			c.InternalToken = devInternalToken
		}
	} else if c.Env == Prod && (c.InternalToken == devInternalToken || len(c.InternalToken) < 32) {
		errs = append(errs, errors.New("INTERNAL_TOKEN: must be at least 32 characters and not the dev default in prod"))
	}
	return errs
}

func checkURL(key, v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s: %q is not an http(s) URL", key, v)
	}
	return nil
}

func load[T any](validate func(*T) []error) (T, error) {
	var c T
	var errs []error
	if err := env.Parse(&c); err != nil {
		var agg env.AggregateError
		if errors.As(err, &agg) {
			errs = append(errs, agg.Errors...)
		} else {
			errs = append(errs, err)
		}
	}
	errs = append(errs, validate(&c)...)
	var msgs []string
	for _, e := range errs {
		if e != nil {
			msgs = append(msgs, "  "+e.Error())
		}
	}
	if len(msgs) > 0 {
		return c, fmt.Errorf("invalid configuration:\n%s", strings.Join(msgs, "\n"))
	}
	return c, nil
}
