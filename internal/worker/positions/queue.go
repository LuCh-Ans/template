package positions

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type EnqueueResult string

const (
	Queued    EnqueueResult = "queued"
	Duplicate EnqueueResult = "duplicate"
	Dropped   EnqueueResult = "dropped"
)

type task struct {
	trip        domain.StaleTrip
	scanContext trace.SpanContext
	started     bool // защищён Queue.mu, как и pending
}

// Queue хранит FIFO-канал и дедупликацию ожидающих и выполняемых задач.
type Queue struct {
	mu      sync.Mutex
	tasks   chan *task
	pending map[uuid.UUID]*task
	closed  bool
}

func NewQueue(size int) (*Queue, error) {
	if size <= 0 {
		return nil, fmt.Errorf("position queue size must be positive, got %d", size)
	}
	return &Queue{tasks: make(chan *task, size), pending: make(map[uuid.UUID]*task)}, nil
}

// TryEnqueue не ждёт свободного места. Dropped не оставляет запись в pending:
// поездку можно снова поставить при следующей проверке. После Close также Dropped.
func (q *Queue) TryEnqueue(trip domain.StaleTrip, scanContext trace.SpanContext) EnqueueResult {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return Dropped
	}
	if _, ok := q.pending[trip.ID]; ok {
		return Duplicate
	}

	job := &task{trip: trip, scanContext: scanContext}
	q.pending[trip.ID] = job
	select {
	case q.tasks <- job:
		return Queued
	default:
		delete(q.pending, trip.ID)
		return Dropped
	}
}

// Cancel отменяет только ожидающую задачу. Уже начавшийся Push не прерывается,
// и дедупликация сохраняется до его завершения.
func (q *Queue) Cancel(tripID uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job, ok := q.pending[tripID]; ok && !job.started {
		delete(q.pending, tripID)
	}
}

// Close вызывается после остановки сканера. Читатели могут доработать буфер.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.tasks)
	}
}

// next атомарно начинает задачу либо пропускает отменённую. Сравнение указателей
// не даёт старой задаче из канала подменить новую задачу той же поездки.
func (q *Queue) next(ctx context.Context) (*task, bool) {
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		select {
		case <-ctx.Done():
			return nil, false
		case job, ok := <-q.tasks:
			if !ok {
				return nil, false
			}
			q.mu.Lock()
			current := q.pending[job.trip.ID] == job
			if current {
				job.started = true
			}
			q.mu.Unlock()
			if current {
				return job, true
			}
		}
	}
}

func (q *Queue) finish(job *task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[job.trip.ID] == job {
		delete(q.pending, job.trip.ID)
	}
}
