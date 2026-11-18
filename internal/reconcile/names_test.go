package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

func TestDescribeSourceNamesTheMainInstance(t *testing.T) {
	if got := describeSource(model.LocalAgentID); got != "the main instance" {
		t.Errorf("describeSource(local) = %q", got)
	}
	if got := describeSource("edge1"); got != "agent edge1" {
		t.Errorf("describeSource(edge1) = %q", got)
	}
	owner := model.Owner{ContainerName: "web", Service: "app"}
	if got := describeOwner(owner); got != "container web service app on the main instance" {
		t.Errorf("describeOwner(local) = %q", got)
	}
	owner.AgentID = "edge1"
	if got := describeOwner(owner); got != "container web service app on agent edge1" {
		t.Errorf("describeOwner(agent) = %q", got)
	}
}

func TestZoneRefsNeverShowTheZoneID(t *testing.T) {
	zone := cloudflare.Zone{ID: "zid123", Name: "example.com"}
	row := model.Resource{Hostname: "app.example.com", ZoneID: "zid123"}
	for _, got := range []string{zoneRef(zone), rowZoneRef(row), zoneNotRead(zoneRef(zone)), zoneNotRead(rowZoneRef(row))} {
		if strings.Contains(got, "zid123") {
			t.Errorf("%q shows the zone ID", got)
		}
	}
	if got := zoneRef(zone); got != "zone example.com" {
		t.Errorf("zoneRef = %q", got)
	}
	if got := rowZoneRef(row); got != "the zone of app.example.com" {
		t.Errorf("rowZoneRef = %q", got)
	}
}
