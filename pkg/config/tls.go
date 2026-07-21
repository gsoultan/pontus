package config

// TLS represents the TLS settings.
type TLS struct {
	CertFile           string `json:"cert_file,omitzero" yaml:"cert_file"`
	KeyFile            string `json:"key_file,omitzero" yaml:"key_file"`
	CAFile             string `json:"ca_file,omitzero" yaml:"ca_file"`
	ServerName         string `json:"server_name,omitzero" yaml:"server_name"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitzero" yaml:"insecure_skip_verify"`
}
