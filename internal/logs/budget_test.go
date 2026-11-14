package logs

import (
	"fmt"
	"sync"
	"testing"
)

func TestBudgetEvictsGlobalOldestWithoutLosingGapMetadata(t *testing.T) {
	budget := NewBudget(2 * (256 + 3))
	first, second := NewStore(), NewStore()
	budget.Register(first)
	budget.Register(second)
	first.Append(Entry{ServiceID: "a", Text: "one"})
	second.Append(Entry{ServiceID: "b", Text: "two"})
	first.Append(Entry{ServiceID: "a", Text: "tri"})
	if got := first.Entries(nil); len(got) != 1 || got[0].Text != "tri" {
		t.Fatalf("wrong oldest eviction: %+v", got)
	}
	if got := second.Entries(nil); len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("second lost newer entry: %+v", got)
	}
	page := first.ReadPage("a", 0, 10)
	if !page.Gap || page.Dropped != 1 {
		t.Fatalf("missing eviction accounting: %+v", page)
	}
	second.Append(Entry{ServiceID: "b", Text: "for"})
	if got := second.Entries(nil); len(got) != 1 || got[0].Text != "for" {
		t.Fatalf("wrong cross-store order: %+v", got)
	}
}
func TestBudgetRegistrationAndConcurrentAppends(t *testing.T) {
	budget := NewBudget(8192)
	stores := []*Store{NewStore(), NewStore(), NewStore()}
	stores[0].Append(Entry{ServiceID: "old", Text: "retained before registration"})
	var workers sync.WaitGroup
	for index, store := range stores {
		workers.Add(1)
		go func(index int, store *Store) {
			defer workers.Done()
			for n := range 300 {
				if n == 10 {
					budget.Register(store)
				}
				store.Append(Entry{ServiceID: "service", Text: fmt.Sprint(index, n)})
			}
			budget.Register(store)
		}(index, store)
	}
	workers.Wait()
	bytes := 0
	for _, store := range stores {
		for _, entry := range store.Entries(nil) {
			bytes += 256 + len(entry.Text)
		}
	}
	if bytes > 8192 {
		t.Fatalf("global budget exceeded: %d", bytes)
	}
	budget.Unregister(stores[0])
	budget.Unregister(stores[0])
	budget.mu.Lock()
	remaining := budget.total
	budget.mu.Unlock()
	if remaining < 0 {
		t.Fatal("unregister double accounting")
	}
}
