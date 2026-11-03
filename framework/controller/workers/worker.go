package workers

import "context"

type Worker interface {
	Run(context.Context) error
	Close(context.Context) error
}
