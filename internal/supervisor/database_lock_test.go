package supervisor

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"testing"
	"time"
)

func TestPostgresLockSerializesIndependentOwners(t *testing.T) {
	raw := os.Getenv("SH_PROTOCOL_TEST_PG")
	if raw == "" {
		t.Skip("isolated PostgreSQL fixture not configured")
	}
	spec := model.Service{Env: map[string]string{"DB": raw}, Task: &model.TaskSpec{Lock: &model.TaskLock{Scope: "database"}}}
	spec.Task.Lock.Target.Env = "DB"
	release, e := databaseLock(context.Background(), spec, func() {})
	if e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if other, e := databaseLock(c, spec, func() {}); e == nil {
		other()
		release()
		t.Fatal("second owner acquired occupied database lock")
	}
	release()
	release, e = databaseLock(context.Background(), spec, func() {})
	if e != nil {
		t.Fatal("lock not released", e)
	}
	release()
}
