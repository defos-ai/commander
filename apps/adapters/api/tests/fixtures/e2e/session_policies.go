package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	fsmission "github.com/defos-ai/commander/providers/fs/missions"
	mongomission "github.com/defos-ai/commander/providers/mongo/missions"
	"github.com/defos-ai/commander/providers/mongo/store"
)

func (s *SessionStack) SessionMission(name string, groups ...policies.SessionPolicy) {
	s.t.Helper()
	definition := missions.Mission{Name: name, Objectives: []string{"Exercise controller-managed resource bindings."},
		Policies: &policies.MissionPolicies{Session: groups}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	for _, group := range groups {
		definition.RequiredActions = append(definition.RequiredActions, group.CreateAction)
		definition.OptionalActions = append(definition.OptionalActions, group.UseActions...)
	}
	var catalog missions.MissionStore
	if strings.HasPrefix(s.database, "mongodb") {
		ctx, cancel := context.WithTimeout(s.t.Context(), 10*time.Second)
		defer cancel()
		database, err := store.Open(ctx, s.database)
		if err != nil {
			s.t.Fatal(err)
		}
		defer func() {
			closing, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := database.Close(closing); err != nil {
				s.t.Error(err)
			}
		}()
		catalog = mongomission.NewMissionMongoStore(database)
	} else {
		var err error
		catalog, err = fsmission.NewMissionFsStore(s.database)
		if err != nil {
			s.t.Fatal(err)
		}
	}
	if err := catalog.Create(s.t.Context(), definition); err != nil {
		s.t.Fatal(err)
	}
}
