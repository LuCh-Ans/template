package saveposition_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/saveposition"
)

type transactionContextKey struct{}

func TestSavePositionCancelsTaskOnlyAfterCommit(t *testing.T) {
	failure := errors.New("database failure")
	for _, tt := range []struct {
		name      string
		updateErr error
		insertErr error
		commitErr error
	}{
		{name: "success"},
		{name: "update fails", updateErr: failure},
		{name: "insert fails", insertErr: failure},
		{name: "commit fails", commitErr: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo, tx, tasks := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl), NewMockpositionTasks(ctrl)
			now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			input := domain.Position{TripID: uuid.New(), RecordedAt: now}
			saved := input
			saved.ID = 42
			txCtx := context.WithValue(t.Context(), transactionContextKey{}, true)
			committed := false
			tx.EXPECT().Do(t.Context(), gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				if err := fn(txCtx); err != nil {
					return err
				}
				committed = tt.commitErr == nil
				return tt.commitErr
			})
			update := repo.EXPECT().UpdateLastPosition(txCtx, input.TripID, now).Return(tt.updateErr)
			if tt.updateErr == nil {
				repo.EXPECT().InsertPosition(txCtx, input).Return(saved, tt.insertErr).After(update)
			}
			var wantErr error
			if tt.updateErr != nil || tt.insertErr != nil || tt.commitErr != nil {
				wantErr = failure
			} else {
				tasks.EXPECT().Cancel(input.TripID).Do(func(uuid.UUID) {
					if !committed {
						t.Fatal("task cancelled before commit")
					}
				})
			}
			useCase := saveposition.New(tx, repo, tasks, func() time.Time { return now })
			got, err := useCase.SavePosition(t.Context(), input)
			if !errors.Is(err, wantErr) {
				t.Fatalf("error=%v, want %v", err, wantErr)
			}
			want := saved
			if wantErr != nil {
				want = domain.Position{}
			}
			if got != want {
				t.Fatalf("position=%+v, want %+v", got, want)
			}
		})
	}
}

func TestSavePositionValidationDoesNotCancelTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo, tx, tasks := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl), NewMockpositionTasks(ctrl)
	useCase := saveposition.New(tx, repo, tasks, time.Now)
	if _, err := useCase.SavePosition(t.Context(), domain.Position{}); err == nil {
		t.Fatal("invalid position accepted")
	}
}
