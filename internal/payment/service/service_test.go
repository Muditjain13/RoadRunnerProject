package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

// fakeRepo is an in-memory Repository/TxRepository, same pattern as
// inventory's tests: WithinTx serializes callers with a single mutex,
// proving the service's own idempotency and over-refund logic is
// race-free. It is not a stand-in for MySQL's row locking.
type fakeRepo struct {
	mu               sync.Mutex
	paymentsByID     map[string]Payment
	paymentsByIdemp  map[string]Payment
	refundsByIdemp   map[string]Refund
	refundsByPayment map[string][]Refund
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		paymentsByID:     make(map[string]Payment),
		paymentsByIdemp:  make(map[string]Payment),
		refundsByIdemp:   make(map[string]Refund),
		refundsByPayment: make(map[string][]Refund),
	}
}

func (f *fakeRepo) WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(ctx, f)
}

func (f *fakeRepo) GetPayment(ctx context.Context, paymentID string) (Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.paymentsByID[paymentID]
	if !ok {
		return Payment{}, apperrors.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) GetPaymentByIdempotencyKey(ctx context.Context, key string) (Payment, error) {
	p, ok := f.paymentsByIdemp[key]
	if !ok {
		return Payment{}, apperrors.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) InsertPayment(ctx context.Context, p Payment) error {
	f.paymentsByID[p.ID] = p
	f.paymentsByIdemp[p.IdempotencyKey] = p
	return nil
}

func (f *fakeRepo) GetPaymentForUpdate(ctx context.Context, paymentID string) (Payment, error) {
	p, ok := f.paymentsByID[paymentID]
	if !ok {
		return Payment{}, apperrors.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) GetRefundByIdempotencyKey(ctx context.Context, key string) (Refund, error) {
	r, ok := f.refundsByIdemp[key]
	if !ok {
		return Refund{}, apperrors.ErrNotFound
	}
	return r, nil
}

func (f *fakeRepo) InsertRefund(ctx context.Context, r Refund) error {
	f.refundsByIdemp[r.IdempotencyKey] = r
	f.refundsByPayment[r.PaymentID] = append(f.refundsByPayment[r.PaymentID], r)
	return nil
}

func (f *fakeRepo) SumSucceededRefunds(ctx context.Context, paymentID string) (money.Paise, error) {
	var total money.Paise
	for _, r := range f.refundsByPayment[paymentID] {
		if r.Status == RefundSucceeded {
			total += r.AmountPaise
		}
	}
	return total, nil
}

func TestCharge_Idempotent(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)

	p1, err := svc.Charge(context.Background(), "key-1", "booking-1", "user-1", 10000)
	if err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	if p1.Status != PaymentSucceeded {
		t.Fatalf("Charge() status = %s, want succeeded", p1.Status)
	}

	p2, err := svc.Charge(context.Background(), "key-1", "booking-1", "user-1", 10000)
	if err != nil {
		t.Fatalf("Charge() replay error = %v", err)
	}
	if p2.ID != p1.ID {
		t.Errorf("Charge() replay returned a different payment: %s != %s", p2.ID, p1.ID)
	}
	if len(repo.paymentsByID) != 1 {
		t.Errorf("Charge() replay created %d payments, want 1 (no double charge)", len(repo.paymentsByID))
	}
}

func TestCharge_GatewayDecline(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, WithGateway(func(money.Paise) bool { return false }))

	p, err := svc.Charge(context.Background(), "key-1", "booking-1", "user-1", 10000)
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil (a decline is not a Go error)", err)
	}
	if p.Status != PaymentFailed {
		t.Errorf("Charge() status = %s, want failed", p.Status)
	}
}

func TestCharge_ConcurrentSameIdempotencyKey(t *testing.T) {
	const attempts = 100
	repo := newFakeRepo()
	svc := NewService(repo)

	ids := make([]string, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			p, err := svc.Charge(context.Background(), "shared-key", "booking-1", "user-1", 10000)
			if err != nil {
				t.Errorf("goroutine %d: Charge() error = %v", i, err)
				return
			}
			ids[i] = p.ID
		}(i)
	}
	wg.Wait()

	if len(repo.paymentsByID) != 1 {
		t.Fatalf("created %d payments for one idempotency key, want 1", len(repo.paymentsByID))
	}
	first := ids[0]
	for i, id := range ids {
		if id != first {
			t.Errorf("goroutine %d got payment ID %s, want %s (all callers should see the same charge)", i, id, first)
		}
	}
}

func TestRefund(t *testing.T) {
	tests := []struct {
		name          string
		paymentStatus PaymentStatus
		paymentAmount money.Paise
		priorRefunds  money.Paise
		refundAmount  money.Paise
		wantErr       error
	}{
		{"full refund of succeeded payment", PaymentSucceeded, 10000, 0, 10000, nil},
		{"partial refund", PaymentSucceeded, 10000, 0, 4000, nil},
		{"refund exceeding remaining balance", PaymentSucceeded, 10000, 7000, 4000, apperrors.ErrConflict},
		{"refund of failed payment", PaymentFailed, 10000, 0, 1000, apperrors.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.paymentsByID["p1"] = Payment{ID: "p1", AmountPaise: tt.paymentAmount, Status: tt.paymentStatus}
			if tt.priorRefunds > 0 {
				repo.refundsByPayment["p1"] = []Refund{{ID: "prior", PaymentID: "p1", AmountPaise: tt.priorRefunds, Status: RefundSucceeded}}
			}
			svc := NewService(repo)

			refund, err := svc.Refund(context.Background(), "refund-key", "p1", tt.refundAmount)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Refund() error = %v, want nil", err)
				}
				if refund.Status != RefundSucceeded {
					t.Errorf("Refund() status = %s, want succeeded", refund.Status)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Refund() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestRefund_Idempotent(t *testing.T) {
	repo := newFakeRepo()
	repo.paymentsByID["p1"] = Payment{ID: "p1", AmountPaise: 10000, Status: PaymentSucceeded}
	svc := NewService(repo)

	r1, err := svc.Refund(context.Background(), "refund-key", "p1", 5000)
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	r2, err := svc.Refund(context.Background(), "refund-key", "p1", 5000)
	if err != nil {
		t.Fatalf("Refund() replay error = %v", err)
	}
	if r1.ID != r2.ID {
		t.Errorf("Refund() replay returned a different refund: %s != %s", r2.ID, r1.ID)
	}
	if len(repo.refundsByPayment["p1"]) != 1 {
		t.Errorf("Refund() replay created %d refunds, want 1", len(repo.refundsByPayment["p1"]))
	}
}

// TestRefund_ConcurrentOverRefund proves that concurrent refund attempts
// against the same payment can never together exceed the charged amount:
// 100 goroutines each try to refund the full payment amount, but only one
// can succeed.
func TestRefund_ConcurrentOverRefund(t *testing.T) {
	const attempts = 100
	const amount money.Paise = 10000

	repo := newFakeRepo()
	repo.paymentsByID["p1"] = Payment{ID: "p1", AmountPaise: amount, Status: PaymentSucceeded}
	svc := NewService(repo)

	var succeeded, conflicts int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.Refund(context.Background(), "refund-key-unique-"+strconv.Itoa(i), "p1", amount)
			switch {
			case err == nil:
				atomic.AddInt64(&succeeded, 1)
			case errors.Is(err, apperrors.ErrConflict):
				atomic.AddInt64(&conflicts, 1)
			default:
				t.Errorf("goroutine %d: unexpected error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1 (no over-refund)", succeeded)
	}
	if conflicts != attempts-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, attempts-1)
	}
}
