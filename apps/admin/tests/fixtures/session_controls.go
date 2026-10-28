package fixtures

const (
	ControlSessionID = "7a193730-7bc0-4dcb-a522-1fcc050eb65a"
	ControlScopeID   = "58340060-95ce-4051-b2c1-2cb77e63ece5"
	ControlRequestID = "87e8740f-8333-432f-9944-82d1f06e56ad"
	ControlOwnerID   = "fixture-agent-00000001"
)

func ControlSessionJSON() string {
	return `{"id":"` + ControlSessionID + `","scope_id":"` + ControlScopeID + `","owner_agent_id":"` + ControlOwnerID + `","kind":"shell","label":"Fixture session","state":"connected","disposition":"active","busy":false,"revision":1,"created_at":"2026-10-24T00:00:00Z","updated_at":"2026-10-24T00:00:00Z"}`
}
