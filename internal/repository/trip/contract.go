package trip

import (
	"context"

	"github.com/LuCh-Ans/template/internal/infra/postgres/transaction"
)

type executorProvider interface {
	Executor(ctx context.Context) transaction.Executor
}
