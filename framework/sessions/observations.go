package sessions

// Evidence names authoritative saved work or inventory. Callers cannot patch records.
type Evidence struct{ OperationID, AgentID string }
