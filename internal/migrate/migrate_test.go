package migrate

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigrationsParse(t *testing.T) {
	goose.SetBaseFS(files)
	t.Cleanup(func() { goose.SetBaseFS(nil) })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	migrations, err := goose.CollectMigrations("migrations", 0, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 {
		t.Fatalf("got %d migrations", len(migrations))
	}
	if migrations[0].Version != 1 {
		t.Fatalf("version %d", migrations[0].Version)
	}
}
