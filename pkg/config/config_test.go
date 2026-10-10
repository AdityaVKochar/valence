package config

import (
	"bufio"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLoadAPIDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/valence")
	c, err := LoadAPI()
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != Dev || c.Addr != ":8080" || c.MaxCodeKiB != 64 || c.InternalToken != devInternalToken {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if !c.AutoMigrateEnabled() {
		t.Fatal("auto-migrate should default to on in dev")
	}
}

func TestLoadAPIListsEveryError(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("VALENCE_ENV", "prod")
	t.Setenv("MAX_CODE_KIB", "1000")
	t.Setenv("GITHUB_CLIENT_ID", "abc")
	_, err := LoadAPI()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"DATABASE_URL", "INTERNAL_TOKEN", "MAX_CODE_KIB", "GITHUB_CLIENT_SECRET"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
}

func TestLoadWorker(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/valence")
	t.Setenv("JUDGE_PROVIDER", "docker")
	if _, err := LoadWorker(); err == nil || !strings.Contains(err.Error(), "JUDGE_PROVIDER") {
		t.Fatalf("expected JUDGE_PROVIDER error, got %v", err)
	}
	t.Setenv("JUDGE_PROVIDER", "local-unsafe")
	c, err := LoadWorker()
	if err != nil {
		t.Fatal(err)
	}
	if c.Slots < 1 {
		t.Fatalf("slots = %d", c.Slots)
	}
}

func TestEnvExampleInSync(t *testing.T) {
	f, err := os.Open("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var inFile []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s.Text()), "#"))
		if k, _, ok := strings.Cut(line, "="); ok && k == strings.ToUpper(k) && !strings.Contains(k, " ") {
			inFile = append(inFile, strings.TrimSpace(k))
		}
	}
	var inCode []string
	for _, v := range []any{API{}, Worker{}} {
		inCode = append(inCode, envKeys(reflect.TypeOf(v))...)
	}
	for _, k := range inCode {
		if !slices.Contains(inFile, k) {
			t.Errorf(".env.example is missing %s", k)
		}
	}
	for _, k := range inFile {
		if !slices.Contains(inCode, k) {
			t.Errorf(".env.example has %s, which no config struct reads", k)
		}
	}
}

func envKeys(t reflect.Type) []string {
	var keys []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			keys = append(keys, envKeys(f.Type)...)
			continue
		}
		if tag := f.Tag.Get("env"); tag != "" {
			keys = append(keys, strings.Split(tag, ",")[0])
		}
	}
	return keys
}
