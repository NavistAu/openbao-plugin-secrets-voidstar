package backend

import (
	"context"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// newTestBackend builds a *Backend directly (bypassing Factory) so
// later tests can reach in and set up fakes before exercising
// requests.
func newTestBackend(t *testing.T) (*Backend, logical.Storage) {
	t.Helper()

	b := newBackend()
	conf := logical.TestBackendConfig()
	conf.StorageView = &logical.InmemStorage{}
	if err := b.Setup(context.Background(), conf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return b, conf.StorageView
}

func TestFactory_Instantiates(t *testing.T) {
	conf := logical.TestBackendConfig()
	conf.StorageView = &logical.InmemStorage{}

	b, err := Factory(context.Background(), conf)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if b == nil {
		t.Fatal("Factory returned a nil backend")
	}
}
