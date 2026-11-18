package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

func shutdown(ctx context.Context, srv *http.Server, closeInfrastructure func(context.Context) error) error {
	// Close пула может блокироваться без поддержки контекста. По дедлайну
	// возвращаем ошибку в main, который логирует её и завершает процесс.
	done := make(chan error, 1)
	go func() {
		var result error
		if err := srv.Shutdown(ctx); err != nil {
			result = fmt.Errorf("shutdown HTTP server: %w", err)
			if err := srv.Close(); err != nil {
				result = errors.Join(result, fmt.Errorf("close HTTP server: %w", err))
			}
		}
		if err := closeInfrastructure(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("shutdown infrastructure: %w", err))
		}
		done <- result
	}()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return errors.Join(err, fmt.Errorf("shutdown deadline reached; forcing process exit: %w", ctx.Err()))
		}
		return err
	case <-ctx.Done():
		return fmt.Errorf("shutdown deadline reached; forcing process exit: %w", ctx.Err())
	}
}
