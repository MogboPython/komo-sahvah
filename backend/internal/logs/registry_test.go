package logs

import (
	"strings"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
}

func TestRegistryCreateAndGet(t *testing.T) {
	tests := []struct {
		name     string
		deployID string
	}{
		{name: "simple id", deployID: "deploy-1"},
		{name: "uuid style", deployID: "550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			created := r.Create(tt.deployID)
			if created == nil {
				t.Fatal("Create returned nil broadcaster")
			}

			got, ok := r.Get(tt.deployID)
			if !ok {
				t.Fatal("Get returned ok = false for existing deployment")
			}
			if got != created {
				t.Fatal("Get returned a different broadcaster than Create")
			}
		})
	}
}

func TestRegistryGet(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(r *Registry) string
		deployID string
		wantOK   bool
	}{
		{
			name: "missing deployment",
			setup: func(r *Registry) string {
				return "missing"
			},
			deployID: "missing",
			wantOK:   false,
		},
		{
			name: "existing deployment",
			setup: func(r *Registry) string {
				id := "exists"
				r.Create(id)
				return id
			},
			deployID: "exists",
			wantOK:   true,
		},
		{
			name: "deleted deployment",
			setup: func(r *Registry) string {
				id := "deleted"
				r.Create(id)
				r.Delete(id)
				return id
			},
			deployID: "deleted",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			id := tt.setup(r)
			if tt.deployID != "" {
				id = tt.deployID
			}

			got, ok := r.Get(id)
			if ok != tt.wantOK {
				t.Fatalf("Get(%q) ok = %v, want %v", id, ok, tt.wantOK)
			}
			if tt.wantOK && got == nil {
				t.Fatalf("Get(%q) returned nil broadcaster with ok = true", id)
			}
			if !tt.wantOK && got != nil {
				t.Fatalf("Get(%q) = %v, want nil with ok = false", id, got)
			}
		})
	}
}

func TestRegistryDelete(t *testing.T) {
	r := NewRegistry()
	deployID := "to-delete"
	r.Create(deployID)

	r.Delete(deployID)

	if _, ok := r.Get(deployID); ok {
		t.Fatal("Get returned ok = true after Delete")
	}

	// Deleting again should not panic.
	r.Delete(deployID)
}

func TestRegistryCreateDuplicatePanics(t *testing.T) {
	r := NewRegistry()
	deployID := "duplicate"
	r.Create(deployID)

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic when creating duplicate broadcaster")
		}
		msg, ok := recovered.(string)
		if !ok {
			t.Fatalf("panic value type = %T, want string", recovered)
		}
		if !strings.Contains(msg, deployID) {
			t.Fatalf("panic message = %q, want it to mention deployID %q", msg, deployID)
		}
	}()

	r.Create(deployID)
}
