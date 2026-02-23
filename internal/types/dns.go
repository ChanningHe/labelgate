// Package types provides common type definitions for labelgate.
package types

// DNSRecordType represents a DNS record type.
type DNSRecordType string

const (
	DNSTypeA     DNSRecordType = "A"
	DNSTypeAAAA  DNSRecordType = "AAAA"
	DNSTypeCNAME DNSRecordType = "CNAME"
	DNSTypeTXT   DNSRecordType = "TXT"
	DNSTypeMX    DNSRecordType = "MX"
	DNSTypeSRV   DNSRecordType = "SRV"
	DNSTypeCAA   DNSRecordType = "CAA"
)

// DNSTarget represents the target resolution method.
type DNSTarget string

const (
	// DNSTargetAuto automatically detects public IP.
	DNSTargetAuto DNSTarget = "auto"
	// DNSTargetContainer uses container IP.
	DNSTargetContainer DNSTarget = "container"
)

// DNSService represents a DNS service configuration parsed from labels.
type DNSService struct {
	// ServiceName is the service identifier from label
	ServiceName string `json:"service_name"`

	// Hostname is the full DNS hostname
	Hostname string `json:"hostname"`

	// Type is the DNS record type (A, AAAA, CNAME, etc.)
	Type DNSRecordType `json:"type"`

	// Target is the record target (IP, hostname, or "auto")
	Target string `json:"target"`

	// Proxied indicates if Cloudflare proxy is enabled
	Proxied bool `json:"proxied"`

	// TTL is the record TTL in seconds (0 = auto)
	TTL int `json:"ttl"`

	// Credential is the credential name to use
	Credential string `json:"credential"`

	// Cleanup indicates if record should be deleted when container stops
	Cleanup bool `json:"cleanup"`

	// Comment is an optional comment for the record
	Comment string `json:"comment,omitempty"`

	// Access is the name of the access policy template to apply (optional).
	// References a labelgate.access.<name> definition.
	Access string `json:"access,omitempty"`

	// MX specific fields
	Priority int `json:"priority,omitempty"`

	// SRV specific fields
	Weight int `json:"weight,omitempty"`
	Port   int `json:"port,omitempty"`

	// CAA specific fields
	Flags int    `json:"flags,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// DNSRecord represents a DNS record in Cloudflare.
type DNSRecord struct {
	ID       string        `json:"id"`
	ZoneID   string        `json:"zone_id"`
	ZoneName string        `json:"zone_name"`
	Name     string        `json:"name"`
	Type     DNSRecordType `json:"type"`
	Content  string        `json:"content"`
	Proxied  bool          `json:"proxied"`
	TTL      int           `json:"ttl"`
	Priority int           `json:"priority,omitempty"`
	Comment  string        `json:"comment,omitempty"`
}

// DefaultDNSService returns a DNSService with default values.
func DefaultDNSService() *DNSService {
	return &DNSService{
		Type:       DNSTypeA,
		Target:     string(DNSTargetAuto),
		Proxied:    true,
		TTL:        0, // auto
		Credential: "default",
		Cleanup:    true,
	}
}
