package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentBeatsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("AVURUOBS_CONFIG", path)

	if _, err := Save(Config{URL: "https://saved.example.com", Token: "avurut_saved"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A shared CI runner may carry someone's saved credentials; the job's own
	// secret has to win, or it silently reports on the wrong install.
	t.Setenv(EnvURL, "https://ci.example.com")
	t.Setenv(EnvToken, "avurut_ci")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.URL != "https://ci.example.com" || c.Token != "avurut_ci" {
		t.Errorf("Load = %+v, want the environment's values", c)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("AVURUOBS_CONFIG", filepath.Join(t.TempDir(), "absent.json"))
	t.Setenv(EnvURL, "https://only-env.example.com")
	t.Setenv(EnvToken, "avurut_env")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("environment alone should be a complete configuration: %v", err)
	}
}

func TestSavedCredentialsAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	t.Setenv("AVURUOBS_CONFIG", path)

	// Pre-create it world-readable: a write to an existing file keeps the old
	// mode, so Save has to set it rather than assume.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Save(Config{URL: "https://x.example.com", Token: "avurut_secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credential file mode = %o, want 600 — a token must not be readable by other users", perm)
	}
}

func TestValidateNamesWhatIsMissing(t *testing.T) {
	if err := (Config{Token: "t"}).Validate(); err == nil {
		t.Error("missing URL should be reported")
	}
	if err := (Config{URL: "u"}).Validate(); err == nil {
		t.Error("missing token should be reported")
	}
	if err := (Config{URL: "u", Token: "t"}).Validate(); err != nil {
		t.Errorf("complete config rejected: %v", err)
	}
}

func TestTrailingSlashIsTrimmed(t *testing.T) {
	t.Setenv("AVURUOBS_CONFIG", filepath.Join(t.TempDir(), "c.json"))
	t.Setenv(EnvURL, "https://obs.example.com/")
	t.Setenv(EnvToken, "avurut_x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Otherwise every request path becomes //api/v1/…, which some proxies
	// answer with a redirect the client does not follow with its Authorization
	// header.
	if c.URL != "https://obs.example.com" {
		t.Errorf("URL = %q, want no trailing slash", c.URL)
	}
}
