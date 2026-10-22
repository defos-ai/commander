package controller_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/controllers"
)

func registration() controllers.Controller {
	stamp := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	return controllers.Controller{
		ID: "default-controller-0123abcd", Type: "default", Status: controllers.Ready,
		Server:  controllers.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"},
		Version: "v1", AgentSelector: "*", RuleSelector: "mission-rules", StartedAt: stamp.Add(-time.Minute),
		LastHeartbeat: stamp, LeaseExpiresAt: stamp.Add(45 * time.Second),
	}
}

func TestSnapshotDerivesLeaseWithoutMutatingObservation(t *testing.T) {
	record := registration()
	for _, test := range []struct {
		name   string
		change func(*controllers.Controller)
		now    time.Time
		status controllers.Status
		stale  bool
	}{
		{"before lease", func(*controllers.Controller) {}, record.LeaseExpiresAt.Add(-time.Nanosecond), controllers.Ready, false},
		{"at lease", func(*controllers.Controller) {}, record.LeaseExpiresAt, controllers.Unavailable, true},
		{"after lease", func(*controllers.Controller) {}, record.LeaseExpiresAt.Add(time.Hour), controllers.Unavailable, true},
		{"missing lease", func(r *controllers.Controller) { r.LeaseExpiresAt = time.Time{} }, record.LastHeartbeat, controllers.Unavailable, true},
		{"missing heartbeat", func(r *controllers.Controller) { r.LastHeartbeat = time.Time{} }, record.LastHeartbeat, controllers.Unavailable, true},
		{"stopped", func(r *controllers.Controller) { r.Status = controllers.Stopped }, record.LeaseExpiresAt.Add(time.Hour), controllers.Stopped, false},
		{"clears stale", func(r *controllers.Controller) { r.Stale = true }, record.LastHeartbeat, controllers.Ready, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := record
			test.change(&input)
			before := input
			got := input.Snapshot(test.now)
			if got.Status != test.status || got.Stale != test.stale {
				t.Fatalf("snapshot = %#v", got)
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatal("snapshot mutated the source observation")
			}
		})
	}
}

func TestControllerValidation(t *testing.T) {
	record := registration()
	for _, status := range []controllers.Status{controllers.Starting, controllers.Ready, controllers.Degraded, controllers.Draining, controllers.Stopped, controllers.Unavailable} {
		valid := record
		valid.Status = status
		if err := valid.Validate(); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
	}
	for _, change := range []func(*controllers.Controller){
		func(r *controllers.Controller) { r.ID = "../other" },
		func(r *controllers.Controller) { r.ID = "default-agent-0123abcd" },
		func(r *controllers.Controller) { r.Type = "other" },
		func(r *controllers.Controller) { r.Status = "unknown" },
		func(r *controllers.Controller) { r.StartedAt = time.Time{} },
		func(r *controllers.Controller) { r.LastHeartbeat = time.Time{} },
		func(r *controllers.Controller) { r.LeaseExpiresAt = time.Time{} },
		func(r *controllers.Controller) { r.LastHeartbeat = r.StartedAt.Add(-time.Nanosecond) },
		func(r *controllers.Controller) { r.LeaseExpiresAt = r.LastHeartbeat },
		func(r *controllers.Controller) { r.LeaseExpiresAt = r.LastHeartbeat.Add(-time.Nanosecond) },
		func(r *controllers.Controller) { r.Server.Hostname = "invalid host" },
		func(r *controllers.Controller) { r.Server.OS = "linux\n" },
		func(r *controllers.Controller) { r.StatusReason = "invalid\x00reason" },
		func(r *controllers.Controller) { r.RuleSelector = "invalid\nselector" },
	} {
		invalid := record
		change(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted invalid registration: %#v", invalid)
		}
	}
	record.Status = controllers.Stopped
	record.LeaseExpiresAt = record.LastHeartbeat
	if err := record.Validate(); err != nil {
		t.Fatalf("stopped registration cannot end its lease: %v", err)
	}
}
