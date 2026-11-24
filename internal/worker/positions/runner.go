package positions

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Runner управляет общей жизнью сканера, тикера и пула.
type Runner struct {
	mu         sync.Mutex
	scanner    scannerRunner
	pool       poolRunner
	ticker     Ticker
	started    bool
	stopped    bool
	cancelScan context.CancelFunc
	cancelPool context.CancelFunc
	scanDone   chan struct{}
	poolDone   chan struct{}
	poolErr    error // защищён закрытием poolDone
}

func NewRunner(scanner scannerRunner, pool poolRunner, ticker Ticker) *Runner {
	return &Runner{scanner: scanner, pool: pool, ticker: ticker}
}

// Start получает независимый контекст жизни фоновых компонентов, не контекст
// сигнала ОС: тот нужен main, чтобы сначала дождаться входящих HTTP-запросов.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.stopped {
		return fmt.Errorf("position runner already started or stopped")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scanCtx, cancelScan := context.WithCancel(ctx)
	poolCtx, cancelPool := context.WithCancel(ctx)
	r.cancelScan, r.cancelPool = cancelScan, cancelPool
	r.scanDone, r.poolDone = make(chan struct{}), make(chan struct{})
	r.started = true
	go func() {
		defer close(r.scanDone)
		r.scanner.Run(scanCtx, r.ticker)
	}()
	go func() {
		defer close(r.poolDone)
		r.poolErr = r.pool.Run(poolCtx)
	}()
	return nil
}

// Stop вызывается после остановки HTTP с оставшимся общим бюджетом shutdown.
// Сначала останавливаем сканер (он закрывает очередь), затем ждём пул.
// По дедлайну отменяем Push-вызовы и ждём выхода горутин перед закрытием ресурсов.
// Если зависимость игнорирует контекст, жёсткий бюджет обеспечивает main.
func (r *Runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	r.stopped = true
	if !r.started {
		r.ticker.Stop()
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	r.cancelScan()
	select {
	case <-r.scanDone:
	case <-ctx.Done():
		r.cancelPool()
		<-r.scanDone
	}
	select {
	case <-r.poolDone:
	case <-ctx.Done():
		r.cancelPool()
		<-r.poolDone
	}
	r.cancelPool()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stop position runner: %w", err)
	}
	if r.poolErr != nil && !errors.Is(r.poolErr, context.Canceled) {
		return fmt.Errorf("position worker pool: %w", r.poolErr)
	}
	return nil
}
