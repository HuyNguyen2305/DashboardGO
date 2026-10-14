// Package config loads application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	// Embeds the IANA timezone database so LoadLocation works on Windows.
	_ "time/tzdata"
)

// Config holds all runtime settings. Build it with Load.
type Config struct {
	Env             string
	Port            int
	DatabaseURL     string
	DBMaxConns      int32
	DBMinConns      int32
	DBQueryTimeout  time.Duration
	BusinessTZ      *time.Location
	CORSOrigins     []string
	TrustedProxies  []string
	ShutdownTimeout time.Duration
}

// MaxDBConns caps DB_MAX_CONNS. It keeps the int32 conversion exact and is far above any sane pool size.
const MaxDBConns = 1000

// IsProduction reports whether the app runs with production settings.
func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads settings from the environment, applying defaults for optional values.
// Empty variables are treated as unset.
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		Env:            getString("APP_ENV", "development"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		CORSOrigins:    getList("CORS_ALLOWED_ORIGINS"),
		TrustedProxies: getList("TRUSTED_PROXIES"),
	}
	if cfg.Env != "development" && cfg.Env != "production" {
		errs = append(errs, fmt.Errorf("APP_ENV must be development or production, got %q", cfg.Env))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	var err error
	if cfg.Port, err = getInt("PORT", 8080); err != nil {
		errs = append(errs, err)
	} else if cfg.Port < 1 || cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be between 1 and 65535, got %d", cfg.Port))
	}

	maxConns, err := getInt("DB_MAX_CONNS", 10)
	errs = append(errs, err)
	minConns, err := getInt("DB_MIN_CONNS", 2)
	errs = append(errs, err)
	if maxConns < 1 || maxConns > MaxDBConns || minConns < 0 || minConns > maxConns {
		errs = append(errs, fmt.Errorf("need 0 <= DB_MIN_CONNS (%d) <= DB_MAX_CONNS (%d) and 1 <= DB_MAX_CONNS <= %d",
			minConns, maxConns, MaxDBConns))
	} else {
		cfg.DBMaxConns, cfg.DBMinConns = int32(maxConns), int32(minConns) //nolint:gosec // bounds checked above
	}

	cfg.DBQueryTimeout, err = getDuration("DB_QUERY_TIMEOUT", 5*time.Second)
	errs = append(errs, err)
	cfg.ShutdownTimeout, err = getDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	errs = append(errs, err)

	// The zone name is sent to Postgres to group by local day and month, so it must be
	// an IANA name both sides know; Go's "Local" means nothing to Postgres.
	tzName := getString("BUSINESS_TZ", "Asia/Ho_Chi_Minh")
	if tzName == "Local" {
		errs = append(errs, errors.New(`BUSINESS_TZ must be an IANA zone name like "Asia/Ho_Chi_Minh", not "Local"`))
	} else if cfg.BusinessTZ, err = time.LoadLocation(tzName); err != nil {
		errs = append(errs, fmt.Errorf("BUSINESS_TZ %q: %w", tzName, err))
	}

	for _, o := range cfg.CORSOrigins {
		if err := validateOrigin(o); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range cfg.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err != nil && net.ParseIP(p) == nil {
			errs = append(errs, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP address or CIDR", p))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// validateOrigin accepts exactly what browsers send in the Origin header: scheme://host[:port].
// The CORS middleware compares origins as plain strings, so anything else would silently never match.
func validateOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("CORS_ALLOWED_ORIGINS: %q must look like https://app.example.com (no wildcard, path or trailing slash)", o)
	}
	return nil
}

func getString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("%s must be an integer, got %q", key, v)
	}
	return n, nil
}

func getDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def, fmt.Errorf("%s must be a positive duration like 5s, got %q", key, v)
	}
	return d, nil
}

func getList(key string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
