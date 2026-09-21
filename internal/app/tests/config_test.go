package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/joho/godotenv"

	"interview/internal/app"
	"interview/internal/logging"
)

const testDatabaseURLName = "TEST_DATABASE_URL" // read by the integration tests only, not by the server

// unsetEnv removes the variables for the duration of the test and restores them afterwards.
func unsetEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, had := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if had {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		})
	}
}

func cleanEnv(t *testing.T) { unsetEnv(t, app.EnvNames...) }

// repoFile returns the absolute path of a file in the repository root.
func repoFile(name string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", name)
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_Defaults(t *testing.T) {
	cleanEnv(t)
	t.Setenv(app.EnvDatabaseURL, "postgres://db")

	cfg, err := app.LoadConfig("no-such-file.env")
	if err != nil {
		t.Fatal(err)
	}
	want := app.Config{
		DatabaseURL: "postgres://db",
		Addr:        ":8080",
		GinMode:     "release",
		Log:         logging.Config{Dir: "logs", Level: "info", MaxSizeMB: 100, MaxBackups: 10, MaxAgeDays: 30, Compress: true},
	}
	if cfg != want {
		t.Fatalf("config = %+v\nwant     %+v", cfg, want)
	}
}

func TestLoadConfig_EveryVariableCanBeOverridden(t *testing.T) {
	cleanEnv(t)
	t.Setenv(app.EnvDatabaseURL, "postgres://prod")
	t.Setenv(app.EnvAddr, "127.0.0.1:9000")
	t.Setenv(app.EnvGinMode, "debug")
	t.Setenv(app.EnvLogDir, "/var/log/loans")
	t.Setenv(app.EnvLogLevel, "DEBUG")
	t.Setenv(app.EnvLogMaxSizeMB, "5")
	t.Setenv(app.EnvLogMaxBackups, "2")
	t.Setenv(app.EnvLogMaxAgeDays, "3")
	t.Setenv(app.EnvLogCompress, "false")

	cfg, err := app.LoadConfig("no-such-file.env")
	if err != nil {
		t.Fatal(err)
	}
	want := app.Config{
		DatabaseURL: "postgres://prod",
		Addr:        "127.0.0.1:9000",
		GinMode:     "debug",
		Log:         logging.Config{Dir: "/var/log/loans", Level: "DEBUG", MaxSizeMB: 5, MaxBackups: 2, MaxAgeDays: 3, Compress: false},
	}
	if cfg != want {
		t.Fatalf("config = %+v\nwant     %+v", cfg, want)
	}
}

func TestLoadConfig_DatabaseURLIsRequired(t *testing.T) {
	cleanEnv(t)

	_, err := app.LoadConfig("no-such-file.env")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("got %v, want DATABASE_URL is required", err)
	}
}

func TestLoadConfig_ReportsEveryProblemAtOnce(t *testing.T) {
	cleanEnv(t)
	t.Setenv(app.EnvGinMode, "fast")
	t.Setenv(app.EnvLogLevel, "loud")
	t.Setenv(app.EnvLogMaxSizeMB, "abc")
	t.Setenv(app.EnvLogMaxBackups, "0")
	t.Setenv(app.EnvLogMaxAgeDays, "-3")
	t.Setenv(app.EnvLogCompress, "maybe")

	_, err := app.LoadConfig("no-such-file.env")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range append([]string{}, app.EnvNames...) {
		if name == app.EnvAddr || name == app.EnvLogDir {
			continue // free-form strings, cannot be invalid
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not mention %s: %v", name, err)
		}
	}
}

func TestLoadConfig_ReadsAnEnvFile(t *testing.T) {
	cleanEnv(t)
	path := writeEnvFile(t, "# a comment\nDATABASE_URL=postgres://from-file\nADDR=:9999\nLOG_DIR=\"my logs\"\n")

	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://from-file" || cfg.Addr != ":9999" || cfg.Log.Dir != "my logs" {
		t.Fatalf("config = %+v, want the values from the file", cfg)
	}
}

func TestLoadConfig_RealEnvironmentBeatsTheEnvFile(t *testing.T) {
	cleanEnv(t)
	t.Setenv(app.EnvAddr, ":7777")
	path := writeEnvFile(t, "DATABASE_URL=postgres://from-file\nADDR=:9999\n")

	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":7777" || cfg.DatabaseURL != "postgres://from-file" {
		t.Fatalf("config = %+v, want ADDR from the environment and DATABASE_URL from the file", cfg)
	}
}

func TestLoadConfig_AMissingEnvFileIsFine(t *testing.T) {
	cleanEnv(t)
	t.Setenv(app.EnvDatabaseURL, "postgres://db")

	if _, err := app.LoadConfig(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("a missing .env must not be an error: %v", err)
	}
}

// .env.example is the documentation of the configuration, so it must not drift from the code.
func TestEnvExample_ListsEveryVariableTheCodeReads(t *testing.T) {
	values, err := godotenv.Read(repoFile(".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}

	known := map[string]bool{testDatabaseURLName: true}
	for _, name := range app.EnvNames {
		known[name] = true
		if _, ok := values[name]; !ok {
			t.Errorf(".env.example does not list %s", name)
		}
	}
	if _, ok := values[testDatabaseURLName]; !ok {
		t.Errorf(".env.example does not list %s", testDatabaseURLName)
	}
	for name := range values {
		if !known[name] {
			t.Errorf(".env.example lists %s, which nothing reads", name)
		}
	}
}

func TestEnvExample_IsAValidConfigWithPlaceholderSecrets(t *testing.T) {
	cleanEnv(t)
	path := repoFile(".env.example")

	if _, err := app.LoadConfig(path); err != nil {
		t.Fatalf("copying .env.example to .env must give a loadable config: %v", err)
	}
	values, _ := godotenv.Read(path)
	for _, name := range []string{app.EnvDatabaseURL, testDatabaseURLName} {
		if !strings.Contains(values[name], "USER:PASSWORD") {
			t.Errorf("%s in .env.example must be an obvious placeholder, got %q", name, values[name])
		}
	}
}

func TestGitignore_KeepsSecretsAndLogsOutOfTheRepositoryButKeepsTheExample(t *testing.T) {
	b, err := os.ReadFile(repoFile(".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	lines := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, want := range []string{".env", "logs/", "!.env.example"} {
		if !lines[want] {
			t.Errorf(".gitignore is missing the line %q", want)
		}
	}
}
