# Commander

Terms for coordinating work and agent-owned resources across operations.

## Language

**Runtime session**:
A resource owned by one running agent instance that can remain available across
actions and operations.
_Avoid_: Operation, task, chat session

**Session owner**:
The exact running agent instance responsible for a runtime session. Another
instance of the same agent type is a different owner.
_Avoid_: Agent type, available agent

**Session scope**:
The context within which runtime sessions are associated and replacement is
controlled. A chat can provide a session scope, but other hosts can provide one too.
_Avoid_: Chat ID, operation ID

**Session abandonment**:
An explicit decision to release a lost runtime session's scope for replacement
while preserving the old session and its uncertain outcome. Abandonment does not
confirm that the old resource was closed.
_Avoid_: Closure, reconnection, migration

**Session intent**:
A task's accepted request to create, reuse or use a particular runtime session
within a scope. Reuse continues establishment of an existing resource; ordinary
use requires a connected resource. Intent alone does not establish ownership or readiness.
_Avoid_: Reservation, session state

**Session reservation**:
A claim on a session scope that prevents competing creation while the current
resource or its creation outcome remains unresolved.
_Avoid_: Agent lease, execution checkpoint

**Session binding**:
The association of dispatched work with one runtime session and its exact owner.
Replacing a session does not change an existing binding.
_Avoid_: Agent preference, current session lookup

**Same-agent affinity**:
An operation's requirement that a group of actions run on the same agent instance,
bound by its first dispatched action. Affinity does not establish action order or
the lifetime of a runtime session.
_Avoid_: Runtime session, execution dependency

**Execution checkpoint**:
An immutable fact recorded within one execution to support reconciliation of its
progress and external effects. A checkpoint does not authorize repeating an effect
or transferring a runtime session to another owner.
_Avoid_: Session state, retry permission
