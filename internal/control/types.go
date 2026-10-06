// Package control defines the public session protocol without exposing runtime configuration.
package control

import "time"

type Identity struct {
	WorkspaceID     string   `json:"workspace_id"`
	SessionID       string   `json:"session_id"`
	ProtocolVersion int      `json:"protocol_version"`
	Capabilities    []string `json:"capabilities"`
}

type Snapshot struct {
	Identity    Identity     `json:"identity"`
	Revision    uint64       `json:"revision"`
	ObservedAt  time.Time    `json:"observed_at"`
	Nodes       []Node       `json:"nodes"`
	Processes   []Process    `json:"processes"`
	Containers  []Container  `json:"containers"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Node struct {
	DependsOn      []string `json:"depends_on"`
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	State          string   `json:"state"`
	Reason         string   `json:"reason"`
	Ownership      string   `json:"ownership"`
	AllowedActions []string `json:"allowed_actions"`
	ResourceRefs   []string `json:"resource_refs"`
	Metric         Metric   `json:"metric"`
	MetricSource   string   `json:"metric_source,omitempty"`
	MetricError    string   `json:"metric_error,omitempty"`
	Ports          []Port   `json:"ports"`
}

type Metric struct {
	SampledAt    *time.Time `json:"sampled_at"`
	Known        bool       `json:"known"`
	Partial      bool       `json:"partial"`
	RSSBytes     *uint64    `json:"rss_bytes"`
	CPUPercent   *float64   `json:"cpu_percent"`
	UptimeMillis *int64     `json:"uptime_millis"`
}

type Process struct {
	ID                string `json:"id"`
	PID               int32  `json:"pid"`
	CreatedUnixMillis *int64 `json:"created_unix_millis"`
	Ownership         string `json:"ownership"`
	Metric            Metric `json:"metric"`
}

type Container struct {
	EndpointIdentity   string              `json:"endpoint_identity"`
	ID                 string              `json:"id"`
	ResourceRef        string              `json:"resource_ref"`
	ServiceID          string              `json:"service_id"`
	Name               string              `json:"name"`
	Image              string              `json:"image"`
	State              string              `json:"state"`
	Health             string              `json:"health"`
	Reason             string              `json:"reason"`
	PublishedEndpoints []PublishedEndpoint `json:"published_endpoints"`
	Metric             Metric              `json:"metric"`
	MetricError        string              `json:"metric_error,omitempty"`
}

type PublishedEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Port struct {
	Port         int      `json:"port"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
	ResourceRefs []string `json:"resource_refs"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	File     string `json:"file,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

type PlanRequest struct {
	Action  string   `json:"action"`
	Targets []string `json:"targets"`
	Port    int      `json:"port,omitempty"`
}

type Plan struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Action      string    `json:"action"`
	Targets     []string  `json:"targets"`
	Affected    []string  `json:"affected"`
	ExpiresAt   time.Time `json:"expires_at"`
	Fingerprint string    `json:"fingerprint"`
	Warnings    []string  `json:"warnings"`
}

type SubmitRequest struct {
	PlanID         string `json:"plan_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type Operation struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Action    string         `json:"action"`
	State     string         `json:"state"`
	Results   []TargetResult `json:"results"`
	Error     *APIError      `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type TargetResult struct {
	Target string    `json:"target"`
	State  string    `json:"state"`
	Error  *APIError `json:"error,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }
