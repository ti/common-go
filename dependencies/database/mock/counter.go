package mock

import (
	"context"
	"fmt"
)

// IncrCounter increments a counter
func (m *Mock) IncrCounter(ctx context.Context, counterTable, key string, start, count int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	counterKey := fmt.Sprintf("%s:%s", counterTable, key)

	if _, exists := m.counters[counterKey]; !exists {
		m.counters[counterKey] = start
	}

	m.counters[counterKey] += count
	return nil
}

// DecrCounter decrements a counter
func (m *Mock) DecrCounter(ctx context.Context, counterTable, key string, count int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	counterKey := fmt.Sprintf("%s:%s", counterTable, key)

	if _, exists := m.counters[counterKey]; !exists {
		m.counters[counterKey] = 0
	}

	m.counters[counterKey] -= count
	return nil
}

// GetCounter gets the current counter value
func (m *Mock) GetCounter(ctx context.Context, counterTable, key string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counterKey := fmt.Sprintf("%s:%s", counterTable, key)

	if value, exists := m.counters[counterKey]; exists {
		return value, nil
	}

	return 0, nil
}
