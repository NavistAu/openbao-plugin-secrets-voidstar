package backend

import (
	"context"
	"fmt"
	"sync"
)

// statusError lets tests script an error carrying an HTTP-like status
// code without a live loopback round trip — the fake's substitute for
// a real *api.ResponseError. loopback.go's is403 checks both types, so
// classification logic runs identically against real and fake errors.
type statusError struct {
	code int
	msg  string
}

// newStatusError builds a statusError, defaulting msg to a generic
// "status <code>" string when the caller doesn't need a specific one.
func newStatusError(code int, msg string) error {
	if msg == "" {
		msg = fmt.Sprintf("voidstar: fake loopback: status %d", code)
	}
	return &statusError{code: code, msg: msg}
}

func (e *statusError) Error() string { return e.msg }

// FakeReadResponse scripts one FakeLoopbackClient.Read response.
type FakeReadResponse struct {
	Data      map[string]interface{}
	LeaseID   string
	Renewable bool
	Err       error
}

// FakeLoopbackClient is a scriptable, in-memory LoopbackClient for
// tests (sibling's opclient_fake.go split): callers script per-path
// read responses and per-call errors, and read back call counts. No
// network anywhere in this type.
type FakeLoopbackClient struct {
	mu sync.Mutex

	// Reads, keyed by path, backs Read. A path absent from this map
	// produces an error (mirrors a real "no scripted expectation"
	// mistake loudly rather than silently returning zero values).
	Reads     map[string]FakeReadResponse
	ReadCalls int

	// RevokeLeaseErr, keyed by lease ID, if set, fails that lease's
	// RevokeLease call. Absent means success, matching docs/NOTES.md
	// F1's idempotent-revoke behavior (a bogus/unscripted lease ID
	// revokes without error).
	RevokeLeaseErr   map[string]error
	RevokeLeaseCalls int

	RevokeSelfErr   error
	RevokeSelfCalls int

	// RenewSelfErr, if set, fails RenewSelf. RenewSelfTTL is returned
	// on success.
	RenewSelfErr   error
	RenewSelfTTL   int
	RenewSelfCalls int
}

var _ LoopbackClient = (*FakeLoopbackClient)(nil)

// NewFakeLoopbackClient returns a FakeLoopbackClient with its maps
// initialized.
func NewFakeLoopbackClient() *FakeLoopbackClient {
	return &FakeLoopbackClient{
		Reads:          map[string]FakeReadResponse{},
		RevokeLeaseErr: map[string]error{},
	}
}

func (f *FakeLoopbackClient) Read(ctx context.Context, path string) (map[string]interface{}, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ReadCalls++
	r, ok := f.Reads[path]
	if !ok {
		return nil, "", false, fmt.Errorf("voidstar: fake loopback: no scripted response for %q", path)
	}
	return r.Data, r.LeaseID, r.Renewable, r.Err
}

func (f *FakeLoopbackClient) RevokeLease(ctx context.Context, leaseID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RevokeLeaseCalls++
	return f.RevokeLeaseErr[leaseID]
}

func (f *FakeLoopbackClient) RevokeSelf(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RevokeSelfCalls++
	return f.RevokeSelfErr
}

func (f *FakeLoopbackClient) RenewSelf(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RenewSelfCalls++
	if f.RenewSelfErr != nil {
		return 0, f.RenewSelfErr
	}
	return f.RenewSelfTTL, nil
}
