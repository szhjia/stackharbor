package supervisor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/lib/pq"
	gp "github.com/shirou/gopsutil/v4/process"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"net/url"
	"strings"
	"time"
)

type taskStatus struct {
	SchemaVersion   int      `json:"schema_version"`
	Status          string   `json:"status"`
	Code            string   `json:"code"`
	Message         string   `json:"message"`
	Remediation     string   `json:"remediation"`
	CheckedScope    []string `json:"checked_scope"`
	TargetRevisions []string `json:"target_revisions"`
}

func (s *Session) phase(id model.ServiceID, gen uint64, state, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e.gen == gen {
		e.state = state
		e.reason = Redact(e.spec, reason)
		s.event(id, state, reason)
	}
}
func Redact(spec model.Service, line string) string {
	for k, v := range spec.Env {
		if len(v) < 6 {
			continue
		}
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "TOKEN") {
			line = strings.ReplaceAll(line, v, "[redacted]")
		}
		if u, e := url.Parse(v); e == nil && u.User != nil {
			line = strings.ReplaceAll(line, u.User.String(), "[redacted]")
		}
	}
	return line
}
func (s *Session) taskCommand(ctx context.Context, id model.ServiceID, gen uint64, spec model.Service, args []string, phase string) (int, string, error) {
	if e := s.checkInputs(); e != nil {
		return -1, "", e
	}
	select {
	case <-ctx.Done():
		return -1, "", ctx.Err()
	case s.sem <- struct{}{}:
	}
	defer func() { <-s.sem }()
	s.phase(id, gen, phase, "")
	cmdSpec := spec
	cmdSpec.Command = args
	var output strings.Builder
	h, err := s.runner.Start(ctx, cmdSpec, func(stream, line string) {
		line = Redact(spec, line)
		s.store.Append(logs.Entry{ServiceID: id, ProjectID: spec.ProjectID, Stream: phase + "/" + stream, Text: line})
		s.mu.Lock()
		e := s.entries[id]
		s.record(model.Event{Time: time.Now(), ServiceID: id, Kind: "task-log/" + phase + "/" + stream, Message: line, OperationID: e.operationID, AttemptID: e.attemptID})
		s.mu.Unlock()
		if stream == "stdout" && output.Len() < 65536 {
			output.WriteString(line)
			output.WriteByte('\n')
		}
	})
	if err != nil {
		return -1, "", errors.New("Unable to start task command (check executable and environment)")
	}
	s.mu.Lock()
	e := s.entries[id]
	if e.gen != gen {
		s.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h.Stop(cleanup, spec.Stop)
		return -1, "", context.Canceled
	}
	e.handle = h
	s.mu.Unlock()
	var code int
	select {
	case <-ctx.Done():
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r := h.Stop(cleanup, spec.Stop)
		if !r.Complete {
			return -1, "", errors.New("Task cleanup incomplete")
		}
		s.mu.Lock()
		if e.gen == gen {
			e.handle = nil
		}
		s.mu.Unlock()
		return -1, "", ctx.Err()
	case result := <-h.Done():
		code = result.Code
		if result.Err != nil && code == 0 {
			return code, output.String(), errors.New("Task descendant cleanup or output drain failed")
		}
	}
	s.mu.Lock()
	if e.gen == gen && len(h.Identities()) == 0 {
		e.handle = nil
	}
	s.mu.Unlock()
	return code, output.String(), nil
}
func checkScope(v taskStatus, required []string) error {
	for _, scope := range required {
		found := false
		for _, actual := range v.CheckedScope {
			if scope == actual {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("Checked scope does not satisfy contract: %s", scope)
		}
	}
	return nil
}
func parseStatus(code int, text string) (taskStatus, error) {
	var v taskStatus
	if len(text) > 65536 || json.Unmarshal([]byte(text), &v) != nil || v.SchemaVersion != 1 || len(v.CheckedScope) == 0 {
		return v, errors.New("Check did not return a valid schema_version=1 status and checked_scope")
	}
	expected := map[int]string{0: "satisfied", 10: "needed", 20: "drift"}[code]
	if expected == "" || v.Status != expected {
		return v, errors.New("Check result does not match exit code")
	}
	return v, nil
}
func ValidateTaskStatus(code int, text string, required []string) error {
	v, err := parseStatus(code, text)
	if err != nil {
		return err
	}
	return checkScope(v, required)
}
func databaseLock(ctx context.Context, spec model.Service, lost context.CancelFunc) (func(), error) {
	l := spec.Task.Lock
	raw := spec.Env[l.Target.Env]
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || !(u.Scheme == "postgresql" || u.Scheme == "postgres" || u.Scheme == "postgresql+psycopg2") {
		return nil, errors.New("Database lock currently requires an explicit PostgreSQL URL")
	}
	u.Scheme = "postgres"
	q := u.Query()
	q.Set("application_name", "stackharbor-schema-lock")
	if q.Get("sslmode") == "" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		return nil, errors.New("Database lock connection failed")
	}
	db.SetMaxOpenConns(1)
	wait, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	conn, err := db.Conn(wait)
	if err != nil {
		db.Close()
		return nil, errors.New("Unable to connect to migration lock target")
	}
	// Advisory locks are database scoped: all schema-write tasks in this database cooperate.
	digest := sha256.Sum256([]byte("stackharbor/schema-write"))
	key := int64(binary.BigEndian.Uint64(digest[:8]))
	for {
		var held bool
		err = conn.QueryRowContext(wait, "SELECT pg_try_advisory_lock($1)", key).Scan(&held)
		if err != nil {
			conn.Close()
			db.Close()
			return nil, errors.New("Migration lock acquisition failed or timed out")
		}
		if held {
			break
		}
		select {
		case <-wait.Done():
			conn.Close()
			db.Close()
			return nil, wait.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	heartbeat, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-ticker.C:
				ping, c := context.WithTimeout(heartbeat, 2*time.Second)
				err := conn.PingContext(ping)
				c()
				if err != nil {
					lost()
					return
				}
			}
		}
	}()
	return func() {
		stopHeartbeat()
		<-heartbeatDone
		release, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_, _ = conn.ExecContext(release, "SELECT pg_advisory_unlock($1)", key)
		conn.Close()
		db.Close()
	}, nil
}
func (s *Session) runTask(ctx context.Context, id model.ServiceID, gen uint64, spec model.Service, fail func(error)) {
	t := spec.Task
	if t == nil {
		fail(errors.New("Task contract missing"))
		return
	}
	if t.Effect == "schema-write" && (t.Lock == nil || t.Check == nil || t.Verify == nil || t.Policy != "when-needed") {
		fail(errors.New("Write migration contract incomplete"))
		return
	}
	if t.Lock != nil {
		var lost context.CancelFunc
		ctx, lost = context.WithCancel(ctx)
		defer lost()
		s.phase(id, gen, "waiting", "Waiting for database migration lock")
		release, err := databaseLock(ctx, spec, lost)
		if err != nil {
			fail(err)
			return
		}
		defer release()
	}
	duration := time.Duration(t.TimeoutSeconds) * time.Second
	if duration == 0 {
		duration = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var checkedTarget []string
	if t.Policy == "when-needed" {
		code, text, err := s.taskCommand(ctx, id, gen, spec, t.Check.Command, "checking")
		if err != nil {
			fail(err)
			return
		}
		v, err := parseStatus(code, text)
		if err != nil {
			fail(err)
			return
		}
		for _, line := range []string{v.Code + " · " + v.Message, v.Remediation, "Checked scope: " + strings.Join(v.CheckedScope, ", ")} {
			if strings.TrimSpace(line) != "" {
				s.store.Append(logs.Entry{ServiceID: id, ProjectID: spec.ProjectID, Stream: "diagnostic", Text: Redact(spec, line)})
			}
		}
		if err := checkScope(v, t.Check.RequiredScope); err != nil {
			fail(err)
			return
		}
		checkedTarget = v.TargetRevisions
		if code == 20 {
			fail(fmt.Errorf("%s：%s；%s", v.Code, v.Message, v.Remediation))
			return
		}
		if code == 0 {
			s.phase(id, gen, "succeeded", "Already satisfied; execution skipped")
			s.completeNode(id, gen, nil)
			return
		}
		if t.Effect == "schema-write" {
			s.mu.Lock()
			active := []model.ServiceID{}
			for _, dep := range s.graph.Dependents([]model.ServiceID{id}) {
				if s.entries[dep].handle != nil {
					active = append(active, dep)
				}
			}
			s.mu.Unlock()
			if len(active) > 0 {
				fail(fmt.Errorf("Maintenance downtime required; consumers still running: %v", active))
				return
			}
			if err := schemaConsumers(ctx, spec); err != nil {
				fail(err)
				return
			}
		}
	}
	s.mu.Lock()
	if e := s.entries[id]; e.gen == gen {
		e.writeStarted = t.Effect == "schema-write"
	}
	s.mu.Unlock()
	code, _, err := s.taskCommand(ctx, id, gen, spec, spec.Command, "running-task")
	if err != nil || code != 0 {
		if err == nil {
			err = fmt.Errorf("Task exited (%d)", code)
		}
		fail(err)
		if t.Effect == "schema-write" && ctx.Err() != nil {
			s.phase(id, gen, "unknown", "Migration interrupted; database state must be checked again")
		}
		return
	}
	if t.Verify != nil {
		code, text, err := s.taskCommand(ctx, id, gen, spec, t.Verify.Command, "verifying")
		if err != nil {
			fail(err)
			if t.Effect == "schema-write" && ctx.Err() != nil {
				s.phase(id, gen, "unknown", "Migration verification interrupted; database state must be checked again")
			}
			return
		}
		v, parseErr := parseStatus(code, text)
		if parseErr != nil || code != 0 || checkScope(v, t.Verify.RequiredScope) != nil || fmt.Sprint(checkedTarget) != fmt.Sprint(v.TargetRevisions) {
			fail(errors.New("Verification failed; dependents remain blocked"))
			return
		}
	}
	s.phase(id, gen, "succeeded", "Task completed")
	s.mu.Lock()
	if e := s.entries[id]; e.gen == gen {
		e.writeStarted = false
	}
	s.mu.Unlock()
	s.completeNode(id, gen, nil)
}

// A conservative snapshot, not an exclusive ban on future application connections.
func schemaConsumers(ctx context.Context, spec model.Service) error {
	u, e := url.Parse(spec.Env[spec.Task.Lock.Target.Env])
	if e != nil {
		return errors.New("Unable to verify external database consumers")
	}
	u.Scheme = "postgres"
	q := u.Query()
	if q.Get("sslmode") == "" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
		q.Set("sslmode", "disable")
	}
	q.Set("application_name", "stackharbor-consumer-check")
	u.RawQuery = q.Encode()
	db, e := sql.Open("postgres", u.String())
	if e != nil {
		return errors.New("Unable to verify external database consumers")
	}
	defer db.Close()
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var visible bool
	e = db.QueryRowContext(c, `SELECT pg_has_role(current_user,'pg_read_all_stats','member') OR (SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&visible)
	if e != nil || !visible {
		return errors.New("Unable to fully observe database consumers; pg_read_all_stats or equivalent permissions required; migration not executed")
	}
	var count int
	e = db.QueryRowContext(c, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend' AND COALESCE(application_name,'')<>'stackharbor-schema-lock'`).Scan(&count)
	if e != nil {
		return errors.New("Unable to verify external database consumers; migration not executed")
	}
	if count > 0 {
		return fmt.Errorf("Maintenance downtime required: database has %d other client connections; migration not executed", count)
	}
	return nil
}
func (s *Session) completeNode(id model.ServiceID, gen uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e.gen == gen {
		s.readyResult(e, err)
	}
}
func (s *Session) runResource(ctx context.Context, id model.ServiceID, gen uint64, spec model.Service, fail func(error)) {
	r := spec.Resource
	m := s.resources[id]
	rows, why := m.Observe(ctx)
	s.rememberResourceObservation(id, gen, r.Service, rows, why, false)
	available, identity := resourceAvailable(r, rows)
	if !available && r.Control == "managed" {
		s.mu.Lock()
		if e := s.entries[id]; e.gen == gen {
			e.resourceMutation = true
		}
		s.mu.Unlock()
		actionErr := m.Action(ctx, "start", []string{r.Service})
		rows, why = m.Observe(ctx)
		s.rememberResourceObservation(id, gen, r.Service, rows, why, true)
		if actionErr != nil {
			fail(actionErr)
			return
		}
		available, identity = resourceAvailable(r, rows)
	}
	if !available {
		fail(fmt.Errorf("Resource %s unavailable or health unknown: %s", spec.Name, why))
		return
	}
	s.rememberResourceObservation(id, gen, r.Service, rows, why, true)
	s.phase(id, gen, "available", "Resource available")
	s.mu.Lock()
	if e := s.entries[id]; e.gen == gen {
		e.resourceIdentity = identity
	}
	s.mu.Unlock()
	s.completeNode(id, gen, nil)
}
func (s *Session) runObserved(ctx context.Context, id model.ServiceID, gen uint64, spec model.Service, fail func(error)) {
	identities, err := observedIdentity(ctx, s.ports, spec)
	if err != nil {
		fail(err)
		return
	}
	if err := observe.CheckHealth(ctx, *spec.Ready); err != nil {
		fail(err)
		return
	}
	s.mu.Lock()
	if e := s.entries[id]; e.gen == gen {
		e.observed = identities
	}
	s.mu.Unlock()
	s.phase(id, gen, "running", "External service · read-only dependency")
	s.completeNode(id, gen, nil)
}
func observedIdentity(ctx context.Context, ports observe.PortProbe, spec model.Service) ([]model.ProcessIdentity, error) {
	if spec.Identity == nil {
		return nil, errors.New("External service identity contract missing")
	}
	ids := []model.ProcessIdentity{}
	rows := ports.Observe(ctx, spec.Ports, nil)
	if len(rows) != len(spec.Ports) {
		return nil, errors.New("External listener status unknown")
	}
	for _, row := range rows {
		if len(row.Listeners) == 0 {
			return nil, errors.New("External service identity unknown")
		}
		for _, listener := range row.Listeners {
			p, e := gp.NewProcessWithContext(ctx, listener.PID)
			if e != nil {
				return nil, errors.New("Unable to read external process identity")
			}
			cwd, e := p.CwdWithContext(ctx)
			if e != nil || cwd != spec.Cwd {
				return nil, errors.New("External process directory does not match")
			}
			argv, e := p.CmdlineSliceWithContext(ctx)
			if e != nil || len(argv) < len(spec.Identity.Command) {
				return nil, errors.New("External process command unknown")
			}
			for i, arg := range spec.Identity.Command {
				if argv[i] != arg {
					return nil, errors.New("External process command does not match")
				}
			}
			info, e := process.NewReader().Read(ctx, listener.PID)
			if e != nil || !process.Live(info) {
				return nil, errors.New("External process identity unknown or process exited")
			}
			ids = append(ids, info.Identity)
		}
	}
	return ids, nil
}
