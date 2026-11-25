package main

import (
	"os"
	"testing"
)

func TestLoadConfigDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VPS_MONITOR_TEST_API_KEY_1", "process-key")
	t.Setenv("MONITOR_USER", "test-user")
	for _, name := range []string{"VPS_MONITOR_TEST_API_KEY_2", "MONITOR_PASSWORD"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}

	const configFile = `{"servers":[
		{"veid":"123","apiKeyEnv":"VPS_MONITOR_TEST_API_KEY_1"},
		{"veid":"456","apiKeyEnv":"VPS_MONITOR_TEST_API_KEY_2"}
	]}`
	if err := os.WriteFile("config.json", []byte(configFile), 0600); err != nil {
		t.Fatal(err)
	}
	const dotEnv = `VPS_MONITOR_TEST_API_KEY_1=file-key
VPS_MONITOR_TEST_API_KEY_2='file-$#=key'
MONITOR_USER=overridden-user
MONITOR_PASSWORD='password-$#='
`
	if err := os.WriteFile(".env", []byte(dotEnv), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig("config.json", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Servers[0].key != "process-key" || c.Servers[1].key != "file-$#=key" {
		t.Fatal("expected the process environment to override .env and .env to fill missing keys")
	}
	if c.user != "test-user" || c.password != "password-$#=" {
		t.Fatal("expected authentication settings to use the same precedence")
	}

	if err := os.Remove(".env"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPS_MONITOR_TEST_API_KEY_2", "second-process-key")
	t.Setenv("MONITOR_PASSWORD", "process-password")
	if _, err := loadConfig("config.json", false, ""); err != nil {
		t.Fatalf("expected startup with process variables alone: %v", err)
	}

	if err := os.WriteFile(".env", []byte("VPS_MONITOR_TEST_API_KEY_1='unterminated-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = loadConfig("config.json", false, "")
	if err == nil || err.Error() != "cannot read or parse .env" {
		t.Fatal("expected a dotenv error that omits file contents")
	}
}
