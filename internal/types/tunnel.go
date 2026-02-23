package types

// TunnelProtocol represents the protocol for tunnel service.
type TunnelProtocol string

const (
	TunnelProtocolHTTP       TunnelProtocol = "http"
	TunnelProtocolHTTPS      TunnelProtocol = "https"
	TunnelProtocolSSH        TunnelProtocol = "ssh"
	TunnelProtocolRDP        TunnelProtocol = "rdp"
	TunnelProtocolTCP        TunnelProtocol = "tcp"
	TunnelProtocolUDP        TunnelProtocol = "udp"
	TunnelProtocolUnix       TunnelProtocol = "unix"
	TunnelProtocolHelloWorld TunnelProtocol = "hello_world"
	TunnelProtocolHTTPStatus TunnelProtocol = "http_status"
)

// TunnelService represents a Tunnel service configuration parsed from labels.
type TunnelService struct {
	// ServiceName is the service identifier from label
	ServiceName string `json:"service_name"`

	// Hostname is the public hostname for the service
	Hostname string `json:"hostname"`

	// Service is the backend service URL (e.g., http://localhost:8080)
	Service string `json:"service"`

	// Tunnel is the tunnel name to use
	Tunnel string `json:"tunnel"`

	// Path is the optional path pattern for routing
	Path string `json:"path,omitempty"`

	// Credential is the credential name to use
	Credential string `json:"credential"`

	// Cleanup indicates if ingress should be deleted when container stops
	Cleanup bool `json:"cleanup"`

	// Access is the name of the access policy template to apply (optional).
	// References a labelgate.access.<name> definition.
	Access string `json:"access,omitempty"`

	// OriginRequest contains origin request configuration
	OriginRequest *OriginRequestConfig `json:"origin_request,omitempty"`
}

// OriginRequestConfig holds origin request configuration for tunnel.
type OriginRequestConfig struct {
	// Connection settings
	ConnectTimeout       string `json:"connect_timeout,omitempty"`
	TLSTimeout           string `json:"tls_timeout,omitempty"`
	TCPKeepAlive         string `json:"tcp_keepalive,omitempty"`
	KeepAliveConnections int    `json:"keep_alive_connections,omitempty"`
	KeepAliveTimeout     string `json:"keep_alive_timeout,omitempty"`

	// TLS settings
	NoTLSVerify      bool   `json:"no_tls_verify,omitempty"`
	OriginServerName string `json:"origin_server_name,omitempty"`
	CAPool           string `json:"ca_pool,omitempty"`

	// HTTP settings
	HTTPHostHeader          string `json:"http_host_header,omitempty"`
	NoHappyEyeballs         bool   `json:"no_happy_eyeballs,omitempty"`
	DisableChunkedEncoding  bool   `json:"disable_chunked_encoding,omitempty"`

	// Protocol settings
	ProxyType string `json:"proxy_type,omitempty"` // "", "socks"
}

// TunnelIngress represents an ingress rule in Cloudflare Tunnel.
type TunnelIngress struct {
	Hostname      string               `json:"hostname,omitempty"`
	Path          string               `json:"path,omitempty"`
	Service       string               `json:"service"`
	OriginRequest *OriginRequestConfig `json:"origin_request,omitempty"`
}

// Tunnel represents a Cloudflare Tunnel.
type Tunnel struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	AccountID       string           `json:"account_id"`
	Status          string           `json:"status"`
	RemoteConfig    bool             `json:"remote_config"`
	ConnectorID     string           `json:"connector_id,omitempty"`
	IngressRules    []*TunnelIngress `json:"ingress_rules,omitempty"`
}

// DefaultTunnelService returns a TunnelService with default values.
func DefaultTunnelService() *TunnelService {
	return &TunnelService{
		Tunnel:     "default",
		Credential: "default",
		Cleanup:    true,
	}
}

// TunnelConfiguration represents a tunnel's complete configuration.
type TunnelConfiguration struct {
	TunnelID string        `json:"tunnel_id"`
	Ingress  []IngressRule `json:"ingress"`
}

// IngressRule represents a single ingress rule in tunnel configuration.
type IngressRule struct {
	Hostname      string               `json:"hostname,omitempty"`
	Path          string               `json:"path,omitempty"`
	Service       string               `json:"service"`
	OriginRequest *OriginRequestConfig `json:"origin_request,omitempty"`
}
