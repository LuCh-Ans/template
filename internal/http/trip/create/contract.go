//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=create_test

package create

import (
	"context"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	createusecase "github.com/LuCh-Ans/template/internal/usecase/trip/create"
)

type tripCreator interface {
	CreateTrip(
		ctx context.Context,
		in createusecase.CreateTripInput,
	) (domain.Trip, bool, error)
}
