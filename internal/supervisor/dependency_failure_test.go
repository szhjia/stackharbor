package supervisor

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestDependencyFailurePublishesBlockedBeforeNotifyingWaiters(t *testing.T) {
	for _, mode := range []string{"failed dependency", "canceled wait"} {
		t.Run(mode, func(t *testing.T) {
			for range 100 {
				ctx, cancel := context.WithCancel(context.Background())
				parent := &entry{state: "failed", reason: "task exited (7)", startErr: errors.New("task failed")}
				if mode == "canceled wait" {
					parent.state = "waiting"
					parent.startErr = nil
					cancel()
				}
				child := &entry{spec: service("app/api", "app/task/check"), state: "waiting", gen: 1, ready: make(chan struct{}), launched: make(chan struct{})}
				s := &Session{entries: map[model.ServiceID]*entry{"app/task/check": parent, child.spec.ID: child}}
				const readers = 8
				observed := make(chan string, readers)
				var waiting sync.WaitGroup
				waiting.Add(readers)
				for range readers {
					go func() {
						waiting.Done()
						<-child.ready
						// Observe as soon as readiness completes, without waiting for
						// launch to return and hide a later state correction.
						s.mu.Lock()
						state := child.state
						if child.startErr == nil || child.reason == "" {
							state = "missing failure details"
						}
						s.mu.Unlock()
						observed <- state
					}()
				}
				waiting.Wait()
				s.workers.Add(1)
				go s.launch(ctx, child.spec.ID, child.gen)
				states := make([]string, readers)
				for i := range states {
					states[i] = <-observed
				}
				s.workers.Wait()
				cancel()
				for _, state := range states {
					if state != "blocked" {
						t.Fatalf("readiness completed before blocked state was published: %s", state)
					}
				}
			}
		})
	}
}
