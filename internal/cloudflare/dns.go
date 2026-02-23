package cloudflare

import (
	"context"
	"fmt"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/dns"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/types"
)

// DNSClient provides DNS record management operations.
type DNSClient struct {
	client *Client
}

// NewDNSClient creates a new DNS client wrapper.
func NewDNSClient(client *Client) *DNSClient {
	return &DNSClient{client: client}
}

// CreateRecord creates a new DNS record.
func (d *DNSClient) CreateRecord(ctx context.Context, record *types.DNSRecord) (*types.DNSRecord, error) {
	zoneID := record.ZoneID
	if zoneID == "" {
		var err error
		zoneID, err = d.client.GetZoneID(ctx, record.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get zone ID: %w", err)
		}
	}

	// Build request body based on record type
	body, err := buildRecordBody(record)
	if err != nil {
		return nil, err
	}

	params := dns.RecordNewParams{
		ZoneID: cf.F(zoneID),
		Body:   body,
	}

	result, err := d.client.API().DNS.Records.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS record: %w", err)
	}

	resultRecord := convertRecordResponse(result, zoneID, record.ZoneName)

	log.Info().
		Str("id", resultRecord.ID).
		Str("name", resultRecord.Name).
		Str("type", string(resultRecord.Type)).
		Str("content", resultRecord.Content).
		Msg("Created DNS record")

	return resultRecord, nil
}

// GetRecord retrieves a DNS record by ID.
func (d *DNSClient) GetRecord(ctx context.Context, zoneID, recordID string) (*types.DNSRecord, error) {
	result, err := d.client.API().DNS.Records.Get(ctx, recordID, dns.RecordGetParams{
		ZoneID: cf.F(zoneID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS record: %w", err)
	}

	return convertRecordResponse(result, zoneID, ""), nil
}

// GetRecordByName retrieves a DNS record by hostname and type.
func (d *DNSClient) GetRecordByName(ctx context.Context, hostname string, recordType types.DNSRecordType) (*types.DNSRecord, error) {
	zoneID, err := d.client.GetZoneID(ctx, hostname)
	if err != nil {
		return nil, err
	}

	params := dns.RecordListParams{
		ZoneID: cf.F(zoneID),
		Name:   cf.F(dns.RecordListParamsName{Exact: cf.F(hostname)}),
		Type:   cf.F(dns.RecordListParamsType(recordType)),
	}

	page, err := d.client.API().DNS.Records.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list DNS records: %w", err)
	}

	if len(page.Result) == 0 {
		return nil, nil // Not found
	}

	return convertRecordResponse(&page.Result[0], zoneID, ""), nil
}

// UpdateRecord updates an existing DNS record.
func (d *DNSClient) UpdateRecord(ctx context.Context, record *types.DNSRecord) (*types.DNSRecord, error) {
	if record.ID == "" {
		return nil, fmt.Errorf("record ID is required for update")
	}

	zoneID := record.ZoneID
	if zoneID == "" {
		var err error
		zoneID, err = d.client.GetZoneID(ctx, record.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get zone ID: %w", err)
		}
	}

	// Build request body based on record type
	body, err := buildUpdateRecordBody(record)
	if err != nil {
		return nil, err
	}

	params := dns.RecordUpdateParams{
		ZoneID: cf.F(zoneID),
		Body:   body,
	}

	result, err := d.client.API().DNS.Records.Update(ctx, record.ID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to update DNS record: %w", err)
	}

	resultRecord := convertRecordResponse(result, zoneID, record.ZoneName)

	log.Info().
		Str("id", resultRecord.ID).
		Str("name", resultRecord.Name).
		Str("content", resultRecord.Content).
		Msg("Updated DNS record")

	return resultRecord, nil
}

// DeleteRecord deletes a DNS record.
func (d *DNSClient) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := d.client.API().DNS.Records.Delete(ctx, recordID, dns.RecordDeleteParams{
		ZoneID: cf.F(zoneID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	log.Info().
		Str("id", recordID).
		Str("zone_id", zoneID).
		Msg("Deleted DNS record")

	return nil
}

// ListRecords lists DNS records for a zone.
func (d *DNSClient) ListRecords(ctx context.Context, zoneID string) ([]*types.DNSRecord, error) {
	page, err := d.client.API().DNS.Records.List(ctx, dns.RecordListParams{
		ZoneID: cf.F(zoneID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list DNS records: %w", err)
	}

	result := make([]*types.DNSRecord, 0, len(page.Result))
	for i := range page.Result {
		result = append(result, convertRecordResponse(&page.Result[i], zoneID, ""))
	}

	return result, nil
}

// buildRecordBody creates the appropriate record body for the record type.
func buildRecordBody(record *types.DNSRecord) (dns.RecordNewParamsBodyUnion, error) {
	ttl := dns.TTL(1) // 1 = auto
	if record.TTL > 0 {
		ttl = dns.TTL(record.TTL)
	}

	switch record.Type {
	case types.DNSTypeA:
		return dns.ARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.ARecordTypeA),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeAAAA:
		return dns.AAAARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.AAAARecordTypeAAAA),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeCNAME:
		return dns.CNAMERecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.CNAMERecordTypeCNAME),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeTXT:
		return dns.TXTRecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.TXTRecordTypeTXT),
			Content: cf.F(record.Content),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeMX:
		return dns.MXRecordParam{
			Name:     cf.F(record.Name),
			TTL:      cf.F(ttl),
			Type:     cf.F(dns.MXRecordTypeMX),
			Content:  cf.F(record.Content),
			Priority: cf.F(float64(record.Priority)),
			Comment:  cf.F(record.Comment),
		}, nil

	case types.DNSTypeCAA:
		return dns.CAARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.CAARecordTypeCAA),
			Comment: cf.F(record.Comment),
			Data: cf.F(dns.CAARecordDataParam{
				Flags: cf.F(float64(0)),
				Tag:   cf.F("issue"),
				Value: cf.F(record.Content),
			}),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported DNS record type: %s", record.Type)
	}
}

// buildUpdateRecordBody creates the appropriate record body for update.
func buildUpdateRecordBody(record *types.DNSRecord) (dns.RecordUpdateParamsBodyUnion, error) {
	ttl := dns.TTL(1) // 1 = auto
	if record.TTL > 0 {
		ttl = dns.TTL(record.TTL)
	}

	switch record.Type {
	case types.DNSTypeA:
		return dns.ARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.ARecordTypeA),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeAAAA:
		return dns.AAAARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.AAAARecordTypeAAAA),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeCNAME:
		return dns.CNAMERecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.CNAMERecordTypeCNAME),
			Content: cf.F(record.Content),
			Proxied: cf.F(record.Proxied),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeTXT:
		return dns.TXTRecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.TXTRecordTypeTXT),
			Content: cf.F(record.Content),
			Comment: cf.F(record.Comment),
		}, nil

	case types.DNSTypeMX:
		return dns.MXRecordParam{
			Name:     cf.F(record.Name),
			TTL:      cf.F(ttl),
			Type:     cf.F(dns.MXRecordTypeMX),
			Content:  cf.F(record.Content),
			Priority: cf.F(float64(record.Priority)),
			Comment:  cf.F(record.Comment),
		}, nil

	case types.DNSTypeCAA:
		return dns.CAARecordParam{
			Name:    cf.F(record.Name),
			TTL:     cf.F(ttl),
			Type:    cf.F(dns.CAARecordTypeCAA),
			Comment: cf.F(record.Comment),
			Data: cf.F(dns.CAARecordDataParam{
				Flags: cf.F(float64(0)),
				Tag:   cf.F("issue"),
				Value: cf.F(record.Content),
			}),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported DNS record type: %s", record.Type)
	}
}

// convertRecordResponse converts API response to our record type.
func convertRecordResponse(resp *dns.RecordResponse, zoneID, zoneName string) *types.DNSRecord {
	return &types.DNSRecord{
		ID:       resp.ID,
		ZoneID:   zoneID,
		ZoneName: zoneName,
		Name:     resp.Name,
		Type:     types.DNSRecordType(resp.Type),
		Content:  resp.Content,
		Proxied:  resp.Proxied,
		TTL:      int(resp.TTL),
		Priority: int(resp.Priority),
		Comment:  resp.Comment,
	}
}
