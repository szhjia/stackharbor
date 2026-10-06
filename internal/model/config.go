package model

import "time"

type ServiceID string
type Requirement struct {
	Node      ServiceID `yaml:"node" json:"node"`
	Condition string    `yaml:"condition" json:"condition"`
}
type TaskCommand struct {
	RequiredScope []string `yaml:"required_scope"`
	Command       []string `yaml:"command" json:"command"`
	SatisfiedExit int      `yaml:"satisfied_exit"`
	NeededExit    int      `yaml:"needed_exit"`
	DriftExit     int      `yaml:"drift_exit"`
	SuccessExit   int      `yaml:"success_exit"`
}
type TaskLock struct {
	Scope  string `yaml:"scope"`
	Target struct {
		Env string `yaml:"env"`
	} `yaml:"target"`
	Schema   string `yaml:"schema"`
	SharedID string `yaml:"shared_id"`
}
type TaskSpec struct {
	Inputs         []string     `yaml:"inputs"`
	Effect         string       `yaml:"effect"`
	Policy         string       `yaml:"policy"`
	TimeoutSeconds int          `yaml:"timeout_seconds"`
	Check          *TaskCommand `yaml:"check"`
	Verify         *TaskCommand `yaml:"verify"`
	Lock           *TaskLock    `yaml:"lock"`
}
type ResourceSpec struct {
	composeDigest string
	composeEnv    []string

	Files            []string `yaml:"files"`
	ProjectDirectory string   `yaml:"project_directory"`
	EnvFiles         []string `yaml:"env_files"`

	Name      string `yaml:"name"`
	Adapter   string `yaml:"adapter"`
	File      string `yaml:"file"`
	Project   string `yaml:"project"`
	Service   string `yaml:"service"`
	Available string `yaml:"available"`
	Lifetime  string `yaml:"lifetime"`
	Control   string `yaml:"control"`
}
type Port struct {
	Name   string `yaml:"name" json:"name"`
	Number int    `yaml:"port" json:"port"`
}
type ReadyProbe struct {
	HTTP           string        `yaml:"http" json:"http,omitempty"`
	TCP            string        `yaml:"tcp" json:"tcp,omitempty"`
	Timeout        time.Duration `yaml:"-" json:"-"`
	TimeoutSeconds int           `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
}
type StopPolicy struct {
	Signal         string        `yaml:"signal"`
	Timeout        time.Duration `yaml:"-"`
	TimeoutSeconds int           `yaml:"timeout_seconds"`
}
type Service struct {
	Identity                              *ObservedIdentity
	InputFiles                            []string
	Kind                                  string
	Version                               int
	Requires                              []Requirement
	Task                                  *TaskSpec
	Resource                              *ResourceSpec
	Control                               string
	EnvFrozen                             bool
	ID                                    ServiceID
	ProjectID, Key, Name, SourceFile, Cwd string
	Command                               []string
	Env                                   map[string]string
	Ports                                 []Port
	Ready                                 *ReadyProbe
	DependsOn                             []ServiceID
	DockerDependsOn                       []string
	Stop                                  StopPolicy
	Open                                  string
}
type ObservedIdentity struct {
	Command []string `yaml:"command"`
}
type Project struct {
	ID, Name, SourceFile string
	Services             []Service
}
type Candidate struct {
	ID, Name, Cwd, Kind string
	SuggestedCommand    []string
	NeedsInput          []string
}
type Workspace struct {
	InputFiles  []string
	Version     int
	Root        string
	Projects    []Project
	Candidates  []Candidate
	Diagnostics []Diagnostic
}

func (w Workspace) Services() []Service {
	out := []Service{}
	for _, p := range w.Projects {
		out = append(out, p.Services...)
	}
	return out
}
func (w Workspace) Invalid() bool {
	for _, d := range w.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// ComposeEvidence carries private validation evidence without serializing environment values.
func (r *ResourceSpec) SetComposeEvidence(digest string, env []string) {
	r.composeDigest = digest
	r.composeEnv = append([]string{}, env...)
}
func (r *ResourceSpec) ComposeEvidence() (string, []string) {
	if r.composeEnv == nil {
		return r.composeDigest, nil
	}
	return r.composeDigest, append([]string{}, r.composeEnv...)
}
