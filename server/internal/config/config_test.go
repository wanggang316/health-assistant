package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_PATH":          "/tmp/h.db",
		"APPLE_TEAM_ID":          "HC438T2B8P",
		"APPLE_BUNDLE_ID":        "com.gumpw.health-assistant",
		"APPLE_SIWA_KEY_ID":      "X9UM3BZKR3",
		"APPLE_SIWA_PRIVATE_KEY": base64.StdEncoding.EncodeToString([]byte("pem")),
		"GOOGLE_IOS_CLIENT_ID":   "id.apps.googleusercontent.com",
		"GEMINI_API_KEY":         "k",
		"S3_BUCKET":              "b",
		"TOKEN_ENCRYPTION_KEY":   base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
}

func TestLoadDefaults(t *testing.T) {
	env := validEnv()
	c, err := load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:3140" || c.RecognitionDailyQuota != 30 || c.RecognitionGlobalDailyLimit != 500 ||
		c.GeminiRecognitionModel != "gemini-3.8-flash" || string(c.AppleSIWAPrivateKey) != "pem" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	env := validEnv()
	delete(env, "DATABASE_PATH")
	delete(env, "GEMINI_API_KEY")
	env["TOKEN_ENCRYPTION_KEY"] = base64.StdEncoding.EncodeToString(make([]byte, 16))
	env["RECOGNITION_DAILY_QUOTA"] = "-1"
	_, err := load(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"DATABASE_PATH", "GEMINI_API_KEY", "TOKEN_ENCRYPTION_KEY", "RECOGNITION_DAILY_QUOTA"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}
