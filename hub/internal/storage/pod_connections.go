package storage

import "time"

// PodRef is the identity recorded on a span. Empty identities are never
// resolved from service names: replicas cannot be distinguished that way.
type PodRef struct {
	Name      string
	Namespace string
	Node      string
	Service   string
}

// PodConnection is a caller-observed request edge. When Target.Name is empty,
// Peer is the recorded destination address, not evidence of public Internet.
type PodConnection struct {
	Source PodRef
	Target PodRef
	Peer   string
	Calls  uint64
	Errors uint64
	P95    time.Duration
}
