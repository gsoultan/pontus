package service

// Service defines the interface for Pontus management operations.
type Service interface {
	Project
	Proxy
	Backend
	Observability
	Cluster
	Auth
	Info
}
