// Package config loads the server configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the full server configuration. See docs/design/technical-design.md §10.4.
type Config struct {
	ListenAddr      string
	DatabasePath    string
	NutritionDBPath string
	LogLevel        string

	AppleTeamID         string
	AppleBundleID       string
	AppleSIWAKeyID      string
	AppleSIWAPrivateKey []byte // PEM of the .p8 key

	GoogleIOSClientID string

	GeminiAPIKey           string
	GeminiRecognitionModel string
	GeminiTextModel        string

	AWSRegion string
	S3Bucket  string

	TokenEncryptionKey []byte // 32 bytes

	RecognitionDailyQuota       int
	RecognitionGlobalDailyLimit int
	FeishuAlertWebhook          string
	FeishuAlertSecret           string
}

// Load reads the configuration from the environment. It returns an error that
// names every missing or invalid variable.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	var errs []error
	str := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}
	required := func(name string) string {
		v := str(name, "")
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
		return v
	}
	integer := func(name string, def int) int {
		v := str(name, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive integer", name))
			return def
		}
		return n
	}
	b64 := func(name string) []byte {
		v := required(name)
		if v == "" {
			return nil
		}
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be base64", name))
		}
		return b
	}

	c := Config{
		ListenAddr:                  str("LISTEN_ADDR", "127.0.0.1:3140"),
		DatabasePath:                required("DATABASE_PATH"),
		NutritionDBPath:             str("NUTRITION_DB_PATH", "./nutrition.db"),
		LogLevel:                    str("LOG_LEVEL", "info"),
		AppleTeamID:                 required("APPLE_TEAM_ID"),
		AppleBundleID:               required("APPLE_BUNDLE_ID"),
		AppleSIWAKeyID:              required("APPLE_SIWA_KEY_ID"),
		AppleSIWAPrivateKey:         b64("APPLE_SIWA_PRIVATE_KEY"),
		GoogleIOSClientID:           required("GOOGLE_IOS_CLIENT_ID"),
		GeminiAPIKey:                required("GEMINI_API_KEY"),
		GeminiRecognitionModel:      str("GEMINI_RECOGNITION_MODEL", "gemini-3.8-flash"),
		GeminiTextModel:             str("GEMINI_TEXT_MODEL", "gemini-3.1-flash-lite"),
		AWSRegion:                   str("AWS_REGION", "us-east-1"),
		S3Bucket:                    required("S3_BUCKET"),
		TokenEncryptionKey:          b64("TOKEN_ENCRYPTION_KEY"),
		RecognitionDailyQuota:       integer("RECOGNITION_DAILY_QUOTA", 30),
		RecognitionGlobalDailyLimit: integer("RECOGNITION_GLOBAL_DAILY_LIMIT", 500),
		FeishuAlertWebhook:          str("FEISHU_ALERT_WEBHOOK", ""),
		FeishuAlertSecret:           str("FEISHU_ALERT_SECRET", ""),
	}
	if c.TokenEncryptionKey != nil && len(c.TokenEncryptionKey) != 32 {
		errs = append(errs, errors.New("TOKEN_ENCRYPTION_KEY must decode to 32 bytes"))
	}
	return c, errors.Join(errs...)
}
