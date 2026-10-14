package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/config"
)

var allKeys = []string{
	"APP_ENV", "PORT", "DATABASE_URL", "DB_MAX_CONNS", "DB_MIN_CONNS",
	"DB_QUERY_TIMEOUT", "SHUTDOWN_TIMEOUT", "BUSINESS_TZ", "CORS_ALLOWED_ORIGINS", "TRUSTED_PROXIES",
}

// setEnv clears every config variable, then applies env. Empty values count as unset.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv(k, env[k])
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/db"})

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "development", cfg.Env)
	assert.False(t, cfg.IsProduction())
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, int32(10), cfg.DBMaxConns)
	assert.Equal(t, int32(2), cfg.DBMinConns)
	assert.Equal(t, 5*time.Second, cfg.DBQueryTimeout)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "Asia/Ho_Chi_Minh", cfg.BusinessTZ.String())
	assert.Empty(t, cfg.CORSOrigins)
	assert.Empty(t, cfg.TrustedProxies, "no proxy is trusted unless configured")

	_, offset := time.Date(2026, 10, 1, 0, 0, 0, 0, cfg.BusinessTZ).Zone()
	assert.Equal(t, 7*60*60, offset, "default business timezone must be GMT+7")
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":              "production",
		"PORT":                 "9000",
		"DATABASE_URL":         "postgres://localhost/db",
		"DB_MAX_CONNS":         "25",
		"DB_MIN_CONNS":         "5",
		"DB_QUERY_TIMEOUT":     "3s",
		"SHUTDOWN_TIMEOUT":     "20s",
		"BUSINESS_TZ":          "UTC",
		"CORS_ALLOWED_ORIGINS": " http://a.test , ,https://b.test:8443 ",
		"TRUSTED_PROXIES":      "10.0.0.0/8, 192.168.1.5,::1",
	})

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.True(t, cfg.IsProduction())
	assert.Equal(t, 9000, cfg.Port)
	assert.Equal(t, int32(25), cfg.DBMaxConns)
	assert.Equal(t, int32(5), cfg.DBMinConns)
	assert.Equal(t, 3*time.Second, cfg.DBQueryTimeout)
	assert.Equal(t, 20*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "UTC", cfg.BusinessTZ.String())
	assert.Equal(t, []string{"http://a.test", "https://b.test:8443"}, cfg.CORSOrigins)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.5", "::1"}, cfg.TrustedProxies)
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{}, "DATABASE_URL is required"},
		{"unknown env", map[string]string{"APP_ENV": "staging"}, "APP_ENV must be"},
		{"non-numeric port", map[string]string{"PORT": "abc"}, "PORT must be an integer"},
		{"port out of range", map[string]string{"PORT": "70000"}, "PORT must be between"},
		{"min above max conns", map[string]string{"DB_MAX_CONNS": "2", "DB_MIN_CONNS": "5"}, "DB_MIN_CONNS"},
		{"zero max conns", map[string]string{"DB_MAX_CONNS": "0", "DB_MIN_CONNS": "0"}, "DB_MAX_CONNS"},
		{"max conns above cap", map[string]string{"DB_MAX_CONNS": "1001"}, "DB_MAX_CONNS <= 1000"},
		{"max conns that wraps int32", map[string]string{"DB_MAX_CONNS": "4294967297", "DB_MIN_CONNS": "0"}, "DB_MAX_CONNS <= 1000"},
		{"wildcard origin", map[string]string{"CORS_ALLOWED_ORIGINS": "*"}, "CORS_ALLOWED_ORIGINS"},
		{"origin with trailing slash", map[string]string{"CORS_ALLOWED_ORIGINS": "http://localhost:5173/"}, "CORS_ALLOWED_ORIGINS"},
		{"origin without scheme", map[string]string{"CORS_ALLOWED_ORIGINS": "localhost:5173"}, "CORS_ALLOWED_ORIGINS"},
		{"origin with path", map[string]string{"CORS_ALLOWED_ORIGINS": "https://app.test/dashboard"}, "CORS_ALLOWED_ORIGINS"},
		{"bad trusted proxy", map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8,proxy.local"}, `"proxy.local" is not an IP`},
		{"bad duration", map[string]string{"DB_QUERY_TIMEOUT": "fast"}, "DB_QUERY_TIMEOUT must be"},
		{"negative duration", map[string]string{"SHUTDOWN_TIMEOUT": "-1s"}, "SHUTDOWN_TIMEOUT must be"},
		{"unknown timezone", map[string]string{"BUSINESS_TZ": "Mars/Olympus"}, "BUSINESS_TZ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"DATABASE_URL": "postgres://localhost/db"}
			for k, v := range tc.env {
				env[k] = v
			}
			if tc.name == "missing database url" {
				delete(env, "DATABASE_URL")
			}
			setEnv(t, env)

			_, err := config.Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadReportsAllErrorsAtOnce(t *testing.T) {
	setEnv(t, map[string]string{"PORT": "abc", "BUSINESS_TZ": "Mars/Olympus"})

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL is required")
	assert.Contains(t, err.Error(), "PORT must be an integer")
	assert.Contains(t, err.Error(), "BUSINESS_TZ")
}

func TestLoadRejectsLocalTimezone(t *testing.T) {
	// Postgres groups by the zone name, and it doesn't know Go's "Local".
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/db", "BUSINESS_TZ": "Local"})

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `not "Local"`)
}
