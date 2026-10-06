package model

import "time"

type ProcessIdentity struct {
	PID           int32
	CreatedMillis int64
}
type ProcessSample struct {
	Identity ProcessIdentity
	Metric   Metric
}
type ServiceSnapshot struct {
	Processes                     []ProcessSample
	Spec                          Service
	State, Reason                 string
	ObservedState, ObservedReason string
	ExitCode                      *int
	Owned                         []ProcessIdentity
	Metric                        Metric
	MetricSource                  string
	MetricError                   string
	Ports                         []PortObservation
	ReadinessChecked              bool
}
type Event struct {
	Time          time.Time
	ServiceID     ServiceID
	Kind, Message string
	OperationID   string `json:"operation_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
}
type DockerSnapshot struct {
	EndpointIdentity                            string
	ComposeProject                              string
	ID                                          string
	Service, Name, Image, State, Health, Reason string
	Ports                                       string
	PublishedEndpoints                          []PublishedEndpoint
	Metric                                      Metric
	MetricError                                 string
}

type Snapshot struct {
	ObservedAt              time.Time
	Docker                  []DockerSnapshot
	DockerFile, DockerError string
	Root                    string
	Projects                []Project
	Candidates              []Candidate
	Services                []ServiceSnapshot
	Tool                    Metric
	Events                  []Event
	Diagnostics             []Diagnostic
}

type PublishedEndpoint struct {
	Host string
	Port int
}
