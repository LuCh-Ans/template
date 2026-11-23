package positions

import "time"

type systemTicker struct {
	ticker *time.Ticker
}

// NewTicker получает положительный POSITION_CHECK_INTERVAL из конфигурации.
func NewTicker(interval time.Duration) Ticker {
	return &systemTicker{ticker: time.NewTicker(interval)}
}

func (t *systemTicker) C() <-chan time.Time { return t.ticker.C }

func (t *systemTicker) Stop() { t.ticker.Stop() }
