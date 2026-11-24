package push

import "fmt"

// TransportError сохраняет причину сетевой ошибки или ошибки чтения ответа.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return fmt.Sprintf("push transport failed: %v", e.Err) }

func (e *TransportError) Unwrap() error { return e.Err }

// StatusError означает, что сервер ответил, но команда не была принята.
type StatusError struct{ StatusCode int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("push service returned HTTP %d", e.StatusCode)
}
