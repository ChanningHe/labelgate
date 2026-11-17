package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// dnsPerPage is the page size for DNS record lists.
const dnsPerPage = 1000

// dnsWire is the Cloudflare DNS record JSON (field names as in cloudflare-go/v6 dns.*RecordParam).
// Numbers are float64 like the SDK's.
type dnsWire struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Content  string   `json:"content,omitempty"`
	Proxied  *bool    `json:"proxied,omitempty"`
	TTL      float64  `json:"ttl"`
	Priority *float64 `json:"priority,omitempty"`
	Comment  string   `json:"comment"`
	Data     *dnsData `json:"data,omitempty"`
}

// dnsData is the structured "data" object of SRV and CAA records.
type dnsData struct {
	Priority *float64 `json:"priority,omitempty"`
	Weight   *float64 `json:"weight,omitempty"`
	Port     *float64 `json:"port,omitempty"`
	Target   string   `json:"target,omitempty"`
	Flags    *float64 `json:"flags,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Value    string   `json:"value,omitempty"`
}

// NormalizeDNSRecord returns rec in the form Cloudflare stores and returns it:
// lower-case name (and hostname targets of CNAME/MX/SRV, without trailing dot),
// proxied only for A/AAAA/CNAME, TTL 1 (automatic) when unset or proxied, and
// type-specific fields zeroed for other types. The production client applies it
// to requests and responses, and cffake to stored records, so comparing a
// normalized desired record with a live one never flaps.
func NormalizeDNSRecord(rec DNSRecord) DNSRecord {
	out := DNSRecord{ID: rec.ID, Name: model.NormalizeHostname(rec.Name), Type: rec.Type, Content: rec.Content, TTL: rec.TTL, Comment: rec.Comment}
	switch rec.Type {
	case model.DNSTypeA, model.DNSTypeAAAA:
		out.Proxied = rec.Proxied
	case model.DNSTypeCNAME:
		out.Proxied = rec.Proxied
		out.Content = model.NormalizeHostname(rec.Content)
	case model.DNSTypeMX:
		out.Content = model.NormalizeHostname(rec.Content)
		out.Priority = rec.Priority
	case model.DNSTypeSRV:
		out.Content = model.NormalizeHostname(rec.Content)
		out.Priority, out.Weight, out.Port = rec.Priority, rec.Weight, rec.Port
	case model.DNSTypeCAA:
		out.Flags, out.Tag = rec.Flags, rec.Tag
	case model.DNSTypeTXT:
		out.Content = NormalizeTXT(rec.Content)
	}
	if out.TTL <= 0 || out.Proxied {
		out.TTL = 1
	}
	return out
}

// NormalizeTXT joins TXT content written as quoted character-strings
// (`"a" "b"`, as Cloudflare returns it) into its plain value (`ab`),
// unescaping \" and \\. Content that is not entirely a sequence of quoted
// strings is returned unchanged.
func NormalizeTXT(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, `"`) {
		return content
	}
	var b strings.Builder
	for s != "" {
		if s[0] != '"' {
			return content
		}
		part, rest, ok := cutQuoted(s[1:])
		if !ok {
			return content
		}
		b.WriteString(part)
		s = strings.TrimLeft(rest, " \t")
		if s != "" && len(rest) == len(s) { // strings must be separated by blanks
			return content
		}
	}
	return b.String()
}

// cutQuoted reads up to the closing quote of a character-string, returning
// its unescaped value and the remainder after the quote.
func cutQuoted(s string) (string, string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return b.String(), s[i+1:], true
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(c)
		}
	}
	return "", "", false
}

func toDNSWire(rec DNSRecord) dnsWire {
	r := NormalizeDNSRecord(rec)
	w := dnsWire{Name: r.Name, Type: string(r.Type), TTL: float64(r.TTL), Comment: r.Comment}
	switch r.Type {
	case model.DNSTypeA, model.DNSTypeAAAA, model.DNSTypeCNAME:
		w.Content, w.Proxied = r.Content, &r.Proxied
	case model.DNSTypeMX:
		w.Content, w.Priority = r.Content, num(r.Priority)
	case model.DNSTypeSRV:
		w.Data = &dnsData{Priority: num(r.Priority), Weight: num(r.Weight), Port: num(r.Port), Target: r.Content}
	case model.DNSTypeCAA:
		w.Data = &dnsData{Flags: num(r.Flags), Tag: r.Tag, Value: r.Content}
	default:
		w.Content = r.Content
	}
	return w
}

func fromDNSWire(w dnsWire) DNSRecord {
	r := DNSRecord{ID: w.ID, Name: w.Name, Type: model.DNSType(w.Type), Content: w.Content, TTL: int(w.TTL), Comment: w.Comment}
	if w.Proxied != nil {
		r.Proxied = *w.Proxied
	}
	r.Priority = intOf(w.Priority)
	if d := w.Data; d != nil {
		switch r.Type {
		case model.DNSTypeSRV:
			r.Priority, r.Weight, r.Port, r.Content = intOf(d.Priority), intOf(d.Weight), intOf(d.Port), d.Target
		case model.DNSTypeCAA:
			r.Flags, r.Tag, r.Content = intOf(d.Flags), d.Tag, d.Value
		}
	}
	return NormalizeDNSRecord(r)
}

func num(i int) *float64 {
	f := float64(i)
	return &f
}

func intOf(f *float64) int {
	if f == nil {
		return 0
	}
	return int(*f)
}

// zoneCred validates the zone and returns its credential.
func (c *Client) zoneCred(zone Zone) (*credEntry, error) {
	if zone.ID == "" {
		return nil, fmt.Errorf("zone %q: empty zone ID: %w", zone.Name, model.ErrInvalid)
	}
	return c.credByName(zone.Credential)
}

func recordsPath(zone Zone) string {
	return fmt.Sprintf("zones/%s/dns_records", url.PathEscape(zone.ID))
}

// ListDNSRecords returns every record of the zone.
func (c *Client) ListDNSRecords(ctx context.Context, zone Zone) ([]DNSRecord, error) {
	cr, err := c.zoneCred(zone)
	if err != nil {
		return nil, err
	}
	wires, err := listAll[dnsWire](ctx, c, cr, "dns.list", recordsPath(zone), dnsPerPage)
	if err != nil {
		return nil, err
	}
	out := make([]DNSRecord, 0, len(wires))
	for _, w := range wires {
		out = append(out, fromDNSWire(w))
	}
	return out, nil
}

// CreateDNSRecord creates rec; "already exists" maps to ErrAlreadyExists.
func (c *Client) CreateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error) {
	return c.writeDNS(ctx, zone, "dns.create", http.MethodPost, recordsPath(zone), rec)
}

// UpdateDNSRecord overwrites record rec.ID (PUT).
func (c *Client) UpdateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error) {
	if rec.ID == "" {
		return DNSRecord{}, fmt.Errorf("dns.update: empty record ID: %w", model.ErrInvalid)
	}
	return c.writeDNS(ctx, zone, "dns.update", http.MethodPut, recordsPath(zone)+"/"+url.PathEscape(rec.ID), rec)
}

func (c *Client) writeDNS(ctx context.Context, zone Zone, op, method, path string, rec DNSRecord) (DNSRecord, error) {
	cr, err := c.zoneCred(zone)
	if err != nil {
		return DNSRecord{}, err
	}
	body, err := json.Marshal(toDNSWire(rec))
	if err != nil {
		return DNSRecord{}, err
	}
	var env envelope[dnsWire]
	if err := c.call(ctx, cr, request{op: op, method: method, path: path, body: body, out: &env}); err != nil {
		return DNSRecord{}, err
	}
	return fromDNSWire(env.Result), nil
}

// DeleteDNSRecord deletes a record; missing -> ErrNotFound.
func (c *Client) DeleteDNSRecord(ctx context.Context, zone Zone, recordID string) error {
	if recordID == "" {
		return fmt.Errorf("dns.delete: empty record ID: %w", model.ErrInvalid)
	}
	cr, err := c.zoneCred(zone)
	if err != nil {
		return err
	}
	var env envelope[json.RawMessage]
	path := recordsPath(zone) + "/" + url.PathEscape(recordID)
	return c.call(ctx, cr, request{op: "dns.delete", method: http.MethodDelete, path: path, out: &env})
}
