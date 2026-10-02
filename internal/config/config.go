// Package config loads service configuration from environment variables.
//
// Every setting has an ECOGO_ prefix. Secrets (database password, Meta App Secret, master key)
// arrive the same way, injected from Kubernetes Secrets by the Helm chart.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string // development, staging, production
	HTTPAddr string // listen address for api or ingest
	LogLevel string

	// DatabaseURL is the application role (ecogo_app, no BYPASSRLS).
	DatabaseURL string
	// MigrationDatabaseURL is the owner role used only by the migrate command.
	MigrationDatabaseURL string

	// PublicAppURL is the dashboard origin, e.g. https://whatsapp.ecogo.co.in. Used in emails and cookies.
	PublicAppURL string
	// CookieSecure marks session cookies Secure; off only for local http development.
	CookieSecure bool
	SessionTTL   time.Duration

	Meta Meta

	// MasterKeys holds the envelope-encryption master keys by version; MasterKeyVersion is the one
	// used for new data keys. Older versions stay so existing ciphertext can still be opened.
	MasterKeys       map[int][]byte
	MasterKeyVersion int

	// AppSecret signs stateless tokens such as email verification links.
	AppSecret []byte

	Mail Mail

	Storage Storage

	Razorpay Razorpay

	Seller Seller
}

// Seller is Ecogo's side of every GST invoice. Invoices are issued only once GSTIN is set.
type Seller struct {
	Name    string
	GSTIN   string // 15 characters; the first two digits are the state code
	Address string
	// SAC is the services accounting code printed on invoices; the company's accountant decides it.
	SAC string
	// GSTRateBP is the GST rate in basis points (1800 = 18%). Plan prices include it.
	GSTRateBP int
}

// Razorpay takes payment for our plans (D9). Billing works without it, but nobody can pay.
type Razorpay struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
	BaseURL       string // https://api.razorpay.com; overridden in tests
}

// Storage is where media files live: an S3-compatible bucket (MinIO in the cluster) when
// Endpoint is set, otherwise a local directory for development.
type Storage struct {
	Endpoint  string // host:port, no scheme
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Dir       string
}

type Meta struct {
	AppID           string
	AppSecret       string
	ConfigID        string // Embedded Signup v4 Configuration ID (Facebook Login for Business)
	GraphAPIVersion string
	GraphBaseURL    string // https://graph.facebook.com; overridden in tests
	VerifyToken     string // webhook GET handshake token
}

type Mail struct {
	SMTPHost string
	SMTPPort int
	Username string
	Password string
	From     string
}

// Load reads configuration from the environment and validates what the given role needs.
func Load() (*Config, error) {
	c := &Config{
		Env:                  env("ECOGO_ENV", "development"),
		HTTPAddr:             env("ECOGO_HTTP_ADDR", ":8080"),
		LogLevel:             env("ECOGO_LOG_LEVEL", "info"),
		DatabaseURL:          os.Getenv("ECOGO_DATABASE_URL"),
		MigrationDatabaseURL: os.Getenv("ECOGO_MIGRATION_DATABASE_URL"),
		PublicAppURL:         strings.TrimRight(env("ECOGO_PUBLIC_APP_URL", "http://localhost:5173"), "/"),
		Meta: Meta{
			AppID:           os.Getenv("ECOGO_META_APP_ID"),
			AppSecret:       os.Getenv("ECOGO_META_APP_SECRET"),
			ConfigID:        os.Getenv("ECOGO_META_CONFIG_ID"),
			GraphAPIVersion: env("ECOGO_META_GRAPH_VERSION", "v24.0"),
			GraphBaseURL:    strings.TrimRight(env("ECOGO_META_GRAPH_BASE_URL", "https://graph.facebook.com"), "/"),
			VerifyToken:     os.Getenv("ECOGO_META_WEBHOOK_VERIFY_TOKEN"),
		},
		Mail: Mail{
			SMTPHost: env("ECOGO_SMTP_HOST", "localhost"),
			Username: os.Getenv("ECOGO_SMTP_USERNAME"),
			Password: os.Getenv("ECOGO_SMTP_PASSWORD"),
			From:     env("ECOGO_MAIL_FROM", "Ecogo WhatsApp <no-reply@ecogo.co.in>"),
		},
		Razorpay: Razorpay{
			KeyID:         os.Getenv("ECOGO_RAZORPAY_KEY_ID"),
			KeySecret:     os.Getenv("ECOGO_RAZORPAY_KEY_SECRET"),
			WebhookSecret: os.Getenv("ECOGO_RAZORPAY_WEBHOOK_SECRET"),
			BaseURL:       strings.TrimRight(env("ECOGO_RAZORPAY_BASE_URL", "https://api.razorpay.com"), "/"),
		},
		Seller: Seller{
			Name:    env("ECOGO_SELLER_NAME", "Ecogo Software Solutions Pvt Ltd"),
			GSTIN:   strings.ToUpper(strings.TrimSpace(os.Getenv("ECOGO_SELLER_GSTIN"))),
			Address: os.Getenv("ECOGO_SELLER_ADDRESS"),
			SAC:     os.Getenv("ECOGO_SELLER_SAC"),
		},
		Storage: Storage{
			Endpoint:  os.Getenv("ECOGO_S3_ENDPOINT"),
			AccessKey: os.Getenv("ECOGO_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("ECOGO_S3_SECRET_KEY"),
			Bucket:    env("ECOGO_S3_BUCKET", "ecogo-media"),
			Dir:       env("ECOGO_MEDIA_DIR", "data/media"),
		},
	}

	var err error
	if c.Seller.GSTRateBP, err = strconv.Atoi(env("ECOGO_GST_RATE_BP", "1800")); err != nil || c.Seller.GSTRateBP < 0 || c.Seller.GSTRateBP > 10000 {
		return nil, fmt.Errorf("ECOGO_GST_RATE_BP must be a number of basis points such as 1800")
	}
	if g := c.Seller.GSTIN; g != "" && !gstinPattern.MatchString(g) {
		return nil, fmt.Errorf("ECOGO_SELLER_GSTIN is not a valid GSTIN")
	}
	if c.CookieSecure, err = strconv.ParseBool(env("ECOGO_COOKIE_SECURE", "true")); err != nil {
		return nil, fmt.Errorf("ECOGO_COOKIE_SECURE: %w", err)
	}
	if c.SessionTTL, err = time.ParseDuration(env("ECOGO_SESSION_IDLE_TTL", "12h")); err != nil {
		return nil, fmt.Errorf("ECOGO_SESSION_IDLE_TTL: %w", err)
	}
	if c.Mail.SMTPPort, err = strconv.Atoi(env("ECOGO_SMTP_PORT", "1025")); err != nil {
		return nil, fmt.Errorf("ECOGO_SMTP_PORT: %w", err)
	}
	if c.Storage.UseSSL, err = strconv.ParseBool(env("ECOGO_S3_USE_SSL", "false")); err != nil {
		return nil, fmt.Errorf("ECOGO_S3_USE_SSL: %w", err)
	}
	if c.MasterKeys, c.MasterKeyVersion, err = parseMasterKeys(os.Getenv("ECOGO_MASTER_KEYS")); err != nil {
		return nil, err
	}
	if s := os.Getenv("ECOGO_APP_SECRET"); s != "" {
		c.AppSecret = []byte(s)
	}
	return c, nil
}

// Require returns an error naming every listed setting that is empty.
func (c *Config) Require(names ...string) error {
	values := map[string]bool{
		"ECOGO_DATABASE_URL":              c.DatabaseURL != "",
		"ECOGO_MIGRATION_DATABASE_URL":    c.MigrationDatabaseURL != "",
		"ECOGO_META_APP_ID":               c.Meta.AppID != "",
		"ECOGO_META_APP_SECRET":           c.Meta.AppSecret != "",
		"ECOGO_META_CONFIG_ID":            c.Meta.ConfigID != "",
		"ECOGO_MASTER_KEYS":               len(c.MasterKeys) > 0,
		"ECOGO_APP_SECRET":                len(c.AppSecret) >= 32,
		"ECOGO_META_WEBHOOK_VERIFY_TOKEN": c.Meta.VerifyToken != "",
	}
	var missing []string
	for _, n := range names {
		if ok, known := values[n]; !known || !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing or invalid configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

// parseMasterKeys parses "1:<base64 32 bytes>,2:<base64 32 bytes>"; the highest version is current.
func parseMasterKeys(s string) (map[int][]byte, int, error) {
	keys := map[int][]byte{}
	current := 0
	if s == "" {
		return keys, 0, nil
	}
	for _, part := range strings.Split(s, ",") {
		v, k, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, 0, fmt.Errorf("ECOGO_MASTER_KEYS: entry must be <version>:<base64 key>")
		}
		ver, err := strconv.Atoi(v)
		if err != nil || ver <= 0 {
			return nil, 0, fmt.Errorf("ECOGO_MASTER_KEYS: bad version %q", v)
		}
		key, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(key) != 32 {
			return nil, 0, fmt.Errorf("ECOGO_MASTER_KEYS: version %d must be 32 bytes, base64-encoded", ver)
		}
		keys[ver] = key
		if ver > current {
			current = ver
		}
	}
	return keys, current, nil
}

var gstinPattern = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
