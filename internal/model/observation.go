package model

import "time"

type Metric struct {
	SampledAt      time.Time
	Known, Partial bool
	RSS            uint64
	CPUPercent     *float64
	Uptime         time.Duration
}
type PortObservation struct {
	Port      int
	Status    string
	Listeners []Listener
	Reason    string
}
type Listener struct {
	PID              int32
	Command, Address string
}

type PortConflict struct {
	Port     int
	Command  string
	Identity ProcessIdentity
}
