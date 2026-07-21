package service

// Manager defines the interface for managing OS services.
type Manager interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error)
	Run() error
}
