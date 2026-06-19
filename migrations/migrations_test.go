package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestInitMigrationDoesNotCascadeDeleteData(t *testing.T) {
	data, err := os.ReadFile("001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToUpper(string(data))
	for _, forbidden := range []string{"ON DELETE CASCADE", "ON DELETE SET NULL"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must not contain %q because task data is retained for analytics/model training", forbidden)
		}
	}
}
