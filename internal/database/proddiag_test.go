package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestProdOpenTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("skip in -short")
	}
	l := logrus.New()
	l.SetLevel(logrus.WarnLevel)
	start := time.Now()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "conversions.db"), l)
	d := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Logf("NewSQLiteStore took %v", d)
}
