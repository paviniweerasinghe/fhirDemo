package store

import (
	"sync"
)

// PatientStore abstracts storing FHIR Patient JSON by id.
type PatientStore interface {
	Put(id string, resource []byte) error
	Get(id string) ([]byte, bool)
	Exists(id string) bool
	Delete(id string) bool
}

type Mem struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMem() *Mem {
	return &Mem{data: make(map[string][]byte)}
}

func (m *Mem) Put(id string, resource []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[id] = resource
	return nil
}

func (m *Mem) Get(id string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.data[id]
	return b, ok
}

func (m *Mem) Exists(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[id]
	return ok
}

func (m *Mem) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[id]; !ok {
		return false
	}
	delete(m.data, id)
	return true
}
