package reconcile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

// Where users fix Cloudflare objects labelgate refuses to touch.
const (
	dnsRecordsPage   = "DNS > Records in the Cloudflare dashboard"
	accessAppsPage   = "Zero Trust > Access > Applications"
	tunnelRoutesPage = "Zero Trust > Networks > Tunnels (published application routes)"
)

// describeKey names a resource for people, e.g. "the A record app.example.com".
func describeKey(k rowKey) string {
	parts := strings.Split(k.Key, "|")
	switch {
	case k.Kind == model.KindDNS && len(parts) >= 2:
		s := "the " + parts[1] + " record " + parts[0]
		if len(parts) > 2 && parts[2] != "" {
			s += " (" + parts[2] + ")"
		}
		return s
	case k.Kind == model.KindTunnel && len(parts) == 3:
		s := "the tunnel route " + parts[1]
		if parts[2] != "" {
			s += " path " + parts[2]
		}
		return s + " on tunnel " + parts[0]
	case k.Kind == model.KindAccess:
		return "the Access application for " + k.Key
	}
	return k.String()
}

// describeClass names a hostname class for people.
func describeClass(class string) string {
	switch {
	case class == classRecords:
		return "DNS records"
	case class == classCNAME:
		return "a CNAME record"
	case strings.HasPrefix(class, classTunnelPrefix):
		return "a route on tunnel " + strings.TrimPrefix(class, classTunnelPrefix)
	}
	return class
}

// notOwnedRecord explains a live DNS record labelgate did not create.
func notOwnedRecord(rec cloudflare.DNSRecord, instanceID string) string {
	msg := fmt.Sprintf("%s: a %s record for %s (id %s) already exists in Cloudflare; delete it under %s or use another hostname",
		model.ConflictNotOwned, rec.Type, rec.Name, rec.ID, dnsRecordsPage)
	return msg + otherInstanceHint(rec.Comment, instanceID)
}

// otherInstanceHint recognises another labelgate instance's ownership
// marker; after a lost database, reusing the old ID re-adopts the records.
func otherInstanceHint(comment, instanceID string) string {
	rest, ok := strings.CutPrefix(comment, model.DNSMarker(""))
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, " ")
	if id == instanceID || !model.ValidInstanceID(id) {
		return ""
	}
	return fmt.Sprintf(" (it was created by labelgate instance %q: if that was this installation before its database was lost, set %s to %q)",
		id, config.KeyWithEnv("instance_id"), id)
}

// markerRemoved explains an owned DNS record whose marker was edited away.
func markerRemoved(rec cloudflare.DNSRecord, instanceID string) string {
	return fmt.Sprintf("%s: the ownership marker was removed from the comment of the %s record %s (id %s) in Cloudflare; "+
		"put %q back at the start of its comment, or delete the record", model.ConflictNotOwned, rec.Type, rec.Name, rec.ID, model.DNSMarker(instanceID))
}

// notOwnedApp explains a live Access application labelgate did not create.
func notOwnedApp(app cloudflare.AccessApp) string {
	return fmt.Sprintf("%s: an Access application for %s (id %s) already exists in Cloudflare; delete it under %s or remove the access label",
		model.ConflictNotOwned, app.Domain, app.ID, accessAppsPage)
}

// appTagRemoved explains an owned Access application whose tag was removed.
func appTagRemoved(app cloudflare.AccessApp, tag string) string {
	return fmt.Sprintf("%s: the tag %q was removed from the Access application for %s (id %s); add the tag back, or delete the application",
		model.ConflictNotOwned, tag, app.Domain, app.ID)
}

// createRefused explains a create that Cloudflare rejected as a duplicate
// of an object labelgate did not see when it planned the change.
func createRefused(what, page string) string {
	return fmt.Sprintf("%s: Cloudflare refused to create %s because a conflicting object already exists; delete it under %s or use another hostname",
		model.ConflictNotOwned, what, page)
}

// notOwnedRoute explains an unmanaged ingress rule on a labelgate hostname.
func notOwnedRoute(tunnelID, host string) string {
	return fmt.Sprintf("%s: tunnel %s already routes %s with a rule labelgate did not create; remove it under %s or use another hostname",
		model.ConflictNotOwned, tunnelID, host, tunnelRoutesPage)
}

// zoneError explains why no zone was found for host. Resolution errors
// from the Cloudflare layer already name the hostname and the fix.
func zoneError(host string, err error) string {
	if errors.Is(err, cloudflare.ErrNoZone) || errors.Is(err, cloudflare.ErrUnknownCredential) {
		return err.Error()
	}
	return fmt.Sprintf("cannot find the Cloudflare zone for %s: %v", host, err)
}

// zoneNotRead reports a zone whose records were not listed this cycle.
func zoneNotRead(zoneID string) string {
	return "the DNS records of zone " + zoneID + " were not read this sync; retrying"
}

// accountNotRead reports an account whose Access objects were not listed this cycle.
func accountNotRead(accountID string) string {
	return "the Access applications of account " + accountID + " were not read this sync; retrying"
}

// dynamicTargetFix is the advice when an A/AAAA target cannot be resolved.
const dynamicTargetFix = "set the record's target label to an IP address instead"
