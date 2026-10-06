package logs

import (
	"container/list"
	"github.com/szhjia/stackharbor/internal/model"
	"sync"
	"time"
)

type Entry struct {
	Sequence     uint64
	Time         time.Time
	ProjectID    string
	ServiceID    model.ServiceID
	Stream, Text string
}
type Store struct {
	mu                sync.Mutex
	all               *list.List
	by                map[model.ServiceID][]*list.Element
	bytes             map[model.ServiceID]int
	total             int
	dropped           map[model.ServiceID]uint64
	version           uint64
	lastEvicted       map[model.ServiceID]uint64
	lastGlobalEvicted uint64
	totalDropped      uint64
}

func NewStore() *Store {
	return &Store{all: list.New(), by: map[model.ServiceID][]*list.Element{}, bytes: map[model.ServiceID]int{}, dropped: map[model.ServiceID]uint64{}, lastEvicted: map[model.ServiceID]uint64{}}
}
func (s *Store) remove(e *list.Element) {
	v := e.Value.(Entry)
	n := len(v.Text) + 256
	s.all.Remove(e)
	s.total -= n
	s.bytes[v.ServiceID] -= n
	s.by[v.ServiceID] = s.by[v.ServiceID][1:]
	s.dropped[v.ServiceID]++
	s.totalDropped++
	if v.Sequence > s.lastEvicted[v.ServiceID] {
		s.lastEvicted[v.ServiceID] = v.Sequence
	}
	if v.Sequence > s.lastGlobalEvicted {
		s.lastGlobalEvicted = v.Sequence
	}
}
func (s *Store) Append(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Text = Sanitize(e.Text)
	e.Text = truncateLine(e.Text)
	s.version++
	e.Sequence = s.version
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	el := s.all.PushBack(e)
	s.by[e.ServiceID] = append(s.by[e.ServiceID], el)
	n := len(e.Text) + 256
	s.total += n
	s.bytes[e.ServiceID] += n
	for len(s.by[e.ServiceID]) > 2000 || s.bytes[e.ServiceID] > 2*1024*1024 {
		s.remove(s.by[e.ServiceID][0])
	}
	for s.total > 16*1024*1024 {
		s.remove(s.all.Front())
	}
}
func (s *Store) Entries(ids []model.ServiceID) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[model.ServiceID]bool{}
	for _, id := range ids {
		set[id] = true
	}
	out := []Entry{}
	for e := s.all.Front(); e != nil; e = e.Next() {
		v := e.Value.(Entry)
		if len(ids) == 0 || set[v.ServiceID] {
			out = append(out, v)
		}
	}
	return out
}
func (s *Store) Dropped(id model.ServiceID) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped[id]
}
func (s *Store) Version() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.version }

// Page is read atomically with the store eviction metadata.
type Page struct {
	Entries    []Entry
	NextCursor uint64
	Gap        bool
	Dropped    uint64
}

// ReadPage distinguishes actual eviction from sequence skips caused by target
// filtering. When a page exhausts matching entries its cursor advances to the
// current store version, avoiding repeated scans of unrelated targets.
func (s *Store) ReadPage(target model.ServiceID, after uint64, limit int) Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	out := Page{Entries: []Entry{}, NextCursor: after}
	if target == "" {
		out.Gap = s.lastGlobalEvicted > after
		out.Dropped = s.totalDropped
	} else {
		out.Gap = s.lastEvicted[target] > after
		out.Dropped = s.dropped[target]
	}
	for e := s.all.Front(); e != nil; e = e.Next() {
		entry := e.Value.(Entry)
		if entry.Sequence <= after || target != "" && entry.ServiceID != target {
			continue
		}
		if len(out.Entries) == limit {
			return out
		}
		out.Entries = append(out.Entries, entry)
		out.NextCursor = entry.Sequence
	}
	if s.version > out.NextCursor {
		out.NextCursor = s.version
	}
	return out
}
