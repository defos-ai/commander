package controllers

import (
	"reflect"
	"testing"
	"time"

	controllermodel "commander/framework/domain/controllers"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDocumentPreservesNanosecondsAndRejectsInvalidPrecision(t *testing.T) {
	heartbeat := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	record := controllermodel.Controller{
		ID: "default-controller-0123abcd", Type: "default", Status: controllermodel.Ready,
		Server:  controllermodel.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"},
		Version: "v1", AgentSelector: "*", StartedAt: heartbeat.Add(-time.Minute),
		LastHeartbeat: heartbeat, LeaseExpiresAt: heartbeat.Add(45*time.Second + time.Nanosecond),
	}
	data, err := bson.Marshal(encode(record))
	if err != nil {
		t.Fatal(err)
	}
	var document controllerDocument
	if err := bson.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	got, err := document.decode()
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("BSON round trip = %#v, %v", got, err)
	}
	document.LastHeartbeat.Nanoseconds = -1
	if _, err := document.decode(); err == nil {
		t.Fatal("decoded invalid timestamp precision")
	}
}
