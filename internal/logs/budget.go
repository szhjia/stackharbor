package logs

import "sync"

// Budget bounds retained log bytes across registered stores. Appends and
// registration take the budget lock before any store lock; readers only take a
// store lock. Eviction shares the existing entries and their gap metadata.
type Budget struct {
	mu       sync.Mutex
	maxBytes int
	total    int
	stores   map[*Store]struct{}
}

func NewBudget(maxBytes int) *Budget {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &Budget{maxBytes: maxBytes, stores: map[*Store]struct{}{}}
}

// Register is idempotent; a store remains attached to its first budget.
func (b *Budget) Register(store *Store) {
	if store == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store.mu.Lock()
	if store.budget.Load() != nil {
		store.mu.Unlock()
		return
	}
	store.budget.Store(b)
	b.stores[store] = struct{}{}
	b.total += store.total
	store.mu.Unlock()
	b.enforceLocked()
}

func (b *Budget) enforceLocked() {
	for b.total > b.maxBytes {
		var oldest *Store
		var order uint64
		for store := range b.stores {
			store.mu.Lock()
			first := store.all.Front()
			if first != nil {
				candidate := first.Value.(Entry).appendOrder
				if oldest == nil || candidate < order {
					oldest = store
					order = candidate
				}
			}
			store.mu.Unlock()
		}
		if oldest == nil {
			return
		}
		oldest.mu.Lock()
		oldest.remove(oldest.all.Front())
		oldest.mu.Unlock()
	}
}

// Unregister releases a closed session's store and its accounting. Call only
// after that session has stopped producing logs.
func (b *Budget) Unregister(store *Store) {
	if store == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.budget.Load() != b {
		return
	}
	b.total -= store.total
	delete(b.stores, store)
	store.budget.Store(nil)
}
