package logs

import "sync"

// Registry is a thread-safe map from deployment ID to Broadcaster.
// It is the single source of truth for active log streams.
type Registry struct {
	m sync.Map
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Create(deployID string) *Broadcaster {
	bc := NewBroadcaster()
	if _, loaded := r.m.LoadOrStore(deployID, bc); loaded {
		panic("logs: broadcaster already exists for deployment " + deployID)
	}
	return bc
}

func (r *Registry) Get(deployID string) (*Broadcaster, bool) {
	v, ok := r.m.Load(deployID)
	if !ok {
		return nil, false
	}
	return v.(*Broadcaster), true
}

func (r *Registry) Delete(deployID string) {
	r.m.Delete(deployID)
}
