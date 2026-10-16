package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

func TestDefaultPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		env         map[string]string
		unsetWorker bool
		want        string
	}{
		{"appends TEST_ENV_NUMBER to test database name when set",
			map[string]string{"APP_ENV": "test", "TEST_ENV_NUMBER": "3", "DATABASE_URL": ""}, false,
			filepath.Join(wd, "db", "frankfurter_test_3.sqlite3")},
		{"does not append suffix when TEST_ENV_NUMBER is empty",
			map[string]string{"APP_ENV": "test", "TEST_ENV_NUMBER": "", "DATABASE_URL": ""}, false,
			filepath.Join(wd, "db", "frankfurter_test.sqlite3")},
		{"does not append suffix when TEST_ENV_NUMBER is unset",
			map[string]string{"APP_ENV": "test", "DATABASE_URL": ""}, true,
			filepath.Join(wd, "db", "frankfurter_test.sqlite3")},
		{"uses DATABASE_URL when set, ignoring TEST_ENV_NUMBER",
			map[string]string{"APP_ENV": "test", "TEST_ENV_NUMBER": "3", "DATABASE_URL": "sqlite://custom.sqlite3"}, false,
			"custom.sqlite3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.unsetWorker {
				t.Setenv("TEST_ENV_NUMBER", "")
				os.Unsetenv("TEST_ENV_NUMBER")
			}
			got, err := db.DefaultPath()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestDefaultPathRejectsNonSQLiteURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/frankfurter")
	if _, err := db.DefaultPath(); err == nil {
		t.Error("no error")
	}
}
