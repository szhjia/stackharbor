package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WorkspaceRecord struct {
	Root      string                  `json:"root"`
	Namespace string                  `json:"namespace"`
	Workspace string                  `json:"workspace,omitempty"`
	State     string                  `json:"state"`
	Session   *supervisor.SessionInfo `json:"session,omitempty"`
	Issues    []string                `json:"issues,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
}
type attempt struct {
	Service   string                  `json:"service"`
	State     string                  `json:"state"`
	Processes []model.ProcessIdentity `json:"processes"`
}
type workspaceState struct {
	Mutation         bool               `json:"mutation"`
	MutationAction   string             `json:"mutation_action,omitempty"`
	MutationTargets  []string           `json:"mutation_targets,omitempty"`
	Record           WorkspaceRecord    `json:"record"`
	Attempts         map[string]attempt `json:"attempts"`
	RecoveryRequired bool               `json:"recovery_required"`
}
type stateStore struct {
	mu        sync.Mutex
	directory string
	entries   map[string]*workspaceState
}

func workspaceKey(namespace, root string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + root))
	return fmt.Sprintf("%x", sum)
}
func readState(dir string) (*stateStore, error) {
	s := &stateStore{directory: dir, entries: map[string]*workspaceState{}}
	file := filepath.Join(dir, "workspaces.json")
	named, statErr := os.Lstat(file)
	if statErr == nil && (!named.Mode().IsRegular() || named.Mode().Perm() != 0600) {
		return nil, fmt.Errorf("unsafe engine catalogue")
	}
	f, err := os.OpenFile(file, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("unsafe engine catalogue")
	}
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil || len(b) > 8*1024*1024 {
		return nil, fmt.Errorf("engine catalogue exceeds limit")
	}
	var doc struct {
		Version    int                        `json:"version"`
		Workspaces map[string]*workspaceState `json:"workspaces"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Version != 1 || doc.Workspaces == nil {
		return nil, fmt.Errorf("unsupported or corrupt engine catalogue; existing records preserved")
	}
	s.entries = doc.Workspaces
	for key, w := range s.entries {
		if w == nil || !filepath.IsAbs(w.Record.Root) || !filepath.IsAbs(w.Record.Namespace) || key != workspaceKey(w.Record.Namespace, w.Record.Root) {
			return nil, fmt.Errorf("invalid workspace catalogue identity")
		}
		if w.Attempts == nil {
			w.Attempts = map[string]attempt{}
		}
		w.Record.Session = nil
		w.Record.State = "offline"
		if len(w.Attempts) > 0 || w.Mutation {
			w.RecoveryRequired = true
		}
		if w.RecoveryRequired {
			w.Record.State = "recovery_required"
			w.Record.Issues = []string{"Previous engine ended without confirmed process cleanup. Review recovery before starting."}
		}
	}
	return s, nil
}
func (s *stateStore) saveLocked() error {
	doc := struct {
		Version    int                        `json:"version"`
		Workspaces map[string]*workspaceState `json:"workspaces"`
	}{1, s.entries}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(data) > 8*1024*1024 {
		return fmt.Errorf("engine catalogue exceeds limit")
	}
	return atomicWrite(filepath.Join(s.directory, "workspaces.json"), data, 0600)
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".engine-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type workspaceJournal struct {
	store *stateStore
	key   string
}

func (j workspaceJournal) BeforeSubmit(request control.PlanRequest) error {
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	w := j.store.entries[j.key]
	if w == nil {
		return fmt.Errorf("workspace record missing")
	}
	if w.RecoveryRequired && (strings.Contains(request.Action, "start") || request.Action == "restart") {
		return fmt.Errorf("workspace requires recovery before startup")
	}
	w.Mutation = true
	w.MutationAction = request.Action
	w.MutationTargets = append([]string{}, request.Targets...)
	return j.store.saveLocked()
}

func (j workspaceJournal) Closed(operation control.Operation) {
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	w := j.store.entries[j.key]
	if w == nil {
		return
	}
	if operation.State == "succeeded" && operation.Error == nil && len(w.Attempts) == 0 {
		w.Mutation = false
		w.MutationAction = ""
		w.MutationTargets = nil
	} else {
		w.RecoveryRequired = true
		w.Record.State = "recovery_required"
	}
	if err := j.store.saveLocked(); err != nil {
		w.RecoveryRequired = true
		w.Record.State = "recovery_required"
	}
}

func (j workspaceJournal) Intent(spec model.Service) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%x", nonce)
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	w := j.store.entries[j.key]
	if w == nil || w.RecoveryRequired {
		return "", fmt.Errorf("workspace requires recovery")
	}
	w.Attempts[id] = attempt{Service: string(spec.ID), State: "launching", Processes: []model.ProcessIdentity{}}
	return id, j.store.saveLocked()
}
func (j workspaceJournal) Launched(id string, p model.ProcessIdentity) error {
	return j.Updated(id, []model.ProcessIdentity{p})
}
func (j workspaceJournal) Updated(id string, ps []model.ProcessIdentity) error {
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	w := j.store.entries[j.key]
	a, ok := w.Attempts[id]
	if !ok {
		return fmt.Errorf("launch intent missing")
	}
	a.State = "running"
	a.Processes = append([]model.ProcessIdentity{}, ps...)
	w.Attempts[id] = a
	return j.store.saveLocked()
}
func (j workspaceJournal) Finished(id string, complete bool) error {
	j.store.mu.Lock()
	defer j.store.mu.Unlock()
	w := j.store.entries[j.key]
	if complete {
		delete(w.Attempts, id)
	} else {
		w.RecoveryRequired = true
		w.Record.State = "recovery_required"
	}
	return j.store.saveLocked()
}
