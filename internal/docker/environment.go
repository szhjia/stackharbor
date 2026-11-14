package docker

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/execenv"
	"os"
	"strings"
	"time"
)

// ValidateEnvironment checks a staged refresh without changing the manager.
// Callers validate every manager before applying any updates, while excluding
// observation and operations. A pinned or owned identity cannot be transferred
// to another Docker endpoint or security context by reloading the environment.
func (m *Manager) ValidateEnvironment(environment []string, identitiesOwned bool) error {
	if !identitiesOwned && m.endpoint == "" && len(m.pinned) == 0 {
		return nil
	}
	previous := m.env
	if previous == nil {
		previous = os.Environ()
	}
	values := func(env []string) map[string]string {
		out := map[string]string{}
		for _, pair := range env {
			key, value, ok := strings.Cut(pair, "=")
			if ok && dockerTargetVariable(key) {
				out[key] = value
			}
		}
		return out
	}
	old, next := values(previous), values(environment)
	for key, value := range old {
		if next[key] != value {
			return fmt.Errorf("Docker target or security environment changed (%s); close and reopen the session", key)
		}
	}
	for key, value := range next {
		if old[key] != value {
			return fmt.Errorf("Docker target or security environment changed (%s); close and reopen the session", key)
		}
	}
	return nil
}

func dockerTargetVariable(key string) bool {
	switch key {
	case "HOME", "USERPROFILE", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION", "DOCKER_AUTH_CONFIG", "DOCKER_CUSTOM_HEADERS", "SSH_AUTH_SOCK", "SSH_AGENT_PID", "SSL_CERT_FILE", "SSL_CERT_DIR":
		return true
	}
	return false
}

// SetEnvironment is used at creation or while a session coordinator excludes
// operations. It preserves pinned identities and never changes global PATH.
func (m *Manager) SetEnvironment(environment []string) {
	m.env = append([]string{}, environment...)
	m.run = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := execenv.CommandContext(ctx, m.env, m.root, "docker", args...)
		cmd.WaitDelay = time.Second
		out, stderr := &limitedBuffer{limit: 8 * 1024 * 1024}, &limitedBuffer{limit: 64 * 1024}
		cmd.Stdout, cmd.Stderr = out, stderr
		if err := cmd.Run(); err != nil {
			return nil, errors.New("Docker action failed; verify Docker readiness and workspace environment")
		}
		if out.exceeded || stderr.exceeded {
			return nil, errors.New("Docker output exceeds limit")
		}
		return out.Bytes(), nil
	}
}
