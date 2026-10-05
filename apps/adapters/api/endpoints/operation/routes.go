package operation

import (
	"net/http"

	"commander/framework/domain/operations"
)

func Register(mux *http.ServeMux, store operations.OperationStore) {
	registerOperations(mux, store)
	registerTasks(mux, store)
	registerEvents(mux, store)
	registerQuestions(mux, store)
}
