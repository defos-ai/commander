# Questions and operator input

Open an operation's **Questions** section in Admin to answer a saved question.
In Playground, use the **Operator input** panel. Choose an offered option or
enter your own non-secret text; answer the existing question rather than asking
another one for the same decision.

## Where questions come from

The model can ask for a decision while planning or reviewing work. A running
action can also ask for information it needs to continue, such as which
environment to inspect or which topics to include in a digest.

An operation-level question holds new assignments. A task-linked question pauses
that task's continuation or waiting execution; unrelated work can continue.
Running tasks are not stopped by a question elsewhere in the operation.

## Answer as an operator

1. Open the active operation in Admin and choose the question to answer, or
   focus Playground's **Operator input** panel.
2. Review its text, options and any existing answer. Enter your answer and submit.
3. Inspect the saved answer if submission is uncertain or another answer arrives.
   Do not repeat a write until you have checked the current state.

Playground also accepts an option number or text through `/answer`:

```text
/answer <question-id> 2
```

Panel and `/answer` submissions go directly to the API as operator answers,
outside the model. Playground uses its Admin API login and prompts locally if
one is missing. The operation retains the saved answer and its submission origin.

Keep passwords, tokens and other credentials out of questions and answers.
Use the separate [secret form](secrets.md) when an action needs a secret value.

## If the operator does not answer

Each question has a `fallback_at` time. The default operator waiting window is
120 seconds; question creators can choose another window with `timeout_seconds`.
Before that time only an operator answer is accepted. Afterward the model may
answer too, and an operator can still answer while the operation is active.

This is not an expiry or an automatic answer. Passing the time leaves the
question pending until an answer is saved; silence does not grant approval.
Terminal operations retain their question history without accepting new answers.

Playground reviews watched questions when their waiting window ends. `/stop`
pauses model review; `/watch <operation-id>` resumes it. For another MCP client,
schedule a fresh `get_operation` at or after `fallback_at`, then call
`answer_question` only if that question is still pending. Commander sends no
fallback-time event that invokes an external model automatically.

## Running actions and completion

An action asking a question remains running while it waits, with its original
task deadline. Answer to resume that execution; if it times out, inspect the
saved task outcome before attempting replacement work.

Question fallback does not apply to operator-controlled completion. With
`policies.completion.decision_by: operator`, review the completion draft and use
**Complete operation** in Admin for explicit approval. See
[missions](missions.md) and [operations](operations.md) for that workflow.
