package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

// fakeRepo is an in-memory Repository/TxRepository for testing the
// service's orchestration logic in isolation. WithinTx holds a single
// mutex for its whole call, which correctly serializes concurrent callers
// (proving the service's own state-machine logic is race-free) but is NOT
// a stand-in for MySQL's row-level FOR UPDATE locking - that guarantee is
// proven separately by the testcontainers-backed integration test, which
// is the one that actually exercises real database contention.
type fakeRepo struct {
	mu             sync.Mutex
	seats          map[string]Seat
	holds          map[string]Hold
	holdSeats      map[string][]SeatRef
	waitlist       map[int64]WaitlistEntry
	nextWaitlistID int64
	outbox         map[int64]OutboxEvent
	nextOutboxID   int64
}

func seatKey(tripID, seatNo string) string { return tripID + "|" + seatNo }

func newFakeRepo(seats []Seat) *fakeRepo {
	f := &fakeRepo{
		seats:     make(map[string]Seat),
		holds:     make(map[string]Hold),
		holdSeats: make(map[string][]SeatRef),
		waitlist:  make(map[int64]WaitlistEntry),
		outbox:    make(map[int64]OutboxEvent),
	}
	for _, s := range seats {
		f.seats[seatKey(s.TripID, s.SeatNo)] = s
	}
	return f
}

func (f *fakeRepo) WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(ctx, f)
}

func (f *fakeRepo) ListExpiredActiveHolds(ctx context.Context, before time.Time, limit int) ([]HoldRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []HoldRef
	for _, h := range f.holds {
		if h.Status == HoldActive && h.ExpiresAt.Before(before) {
			out = append(out, HoldRef{ID: h.ID, TripID: h.TripID})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAvailableSeats(ctx context.Context, tripID, classCode string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seats {
		if s.TripID == tripID && s.ClassCode == classCode && s.Status == SeatAvailable {
			out = append(out, s.SeatNo)
		}
	}
	return out, nil
}

func (f *fakeRepo) LockSeats(ctx context.Context, tripID string, seatNos []string) ([]Seat, error) {
	var out []Seat
	for _, sn := range seatNos {
		if s, ok := f.seats[seatKey(tripID, sn)]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeRepo) MarkSeatsHeld(ctx context.Context, tripID string, seatNos []string, holdID string) error {
	for _, sn := range seatNos {
		k := seatKey(tripID, sn)
		s := f.seats[k]
		s.Status = SeatHeld
		s.HoldID = holdID
		f.seats[k] = s
	}
	return nil
}

func (f *fakeRepo) MarkSeatsAvailableFromHold(ctx context.Context, tripID string, seatNos []string, holdID string) (int64, error) {
	var n int64
	for _, sn := range seatNos {
		k := seatKey(tripID, sn)
		s := f.seats[k]
		if s.HoldID == holdID {
			s.Status = SeatAvailable
			s.HoldID = ""
			f.seats[k] = s
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) MarkSeatsBooked(ctx context.Context, tripID string, seatNos []string, holdID, bookingID string) (int64, error) {
	var n int64
	for _, sn := range seatNos {
		k := seatKey(tripID, sn)
		s := f.seats[k]
		if s.HoldID == holdID {
			s.Status = SeatBooked
			s.HoldID = ""
			s.BookingID = bookingID
			f.seats[k] = s
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) MarkSeatsAvailableFromBooking(ctx context.Context, tripID string, seatNos []string, bookingID string) (int64, error) {
	var n int64
	for _, sn := range seatNos {
		k := seatKey(tripID, sn)
		s := f.seats[k]
		if s.BookingID == bookingID {
			s.Status = SeatAvailable
			s.BookingID = ""
			f.seats[k] = s
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) CreateHold(ctx context.Context, hold Hold) error {
	f.holds[hold.ID] = hold
	return nil
}

func (f *fakeRepo) InsertHoldSeat(ctx context.Context, holdID, tripID, seatNo string) error {
	f.holdSeats[holdID] = append(f.holdSeats[holdID], SeatRef{TripID: tripID, SeatNo: seatNo})
	return nil
}

func (f *fakeRepo) GetHoldForUpdate(ctx context.Context, holdID string) (Hold, error) {
	h, ok := f.holds[holdID]
	if !ok {
		return Hold{}, apperrors.ErrNotFound
	}
	return h, nil
}

func (f *fakeRepo) GetHoldSeats(ctx context.Context, holdID string) ([]SeatRef, error) {
	return f.holdSeats[holdID], nil
}

func (f *fakeRepo) UpdateHoldStatus(ctx context.Context, holdID string, status HoldStatus) error {
	h := f.holds[holdID]
	h.Status = status
	f.holds[holdID] = h
	return nil
}

func (f *fakeRepo) LockAvailableSeatsByClass(ctx context.Context, tripID, classCode string, count int) ([]string, error) {
	var candidates []string
	for _, s := range f.seats {
		if s.TripID == tripID && s.ClassCode == classCode && s.Status == SeatAvailable {
			candidates = append(candidates, s.SeatNo)
		}
	}
	sort.Strings(candidates)
	if len(candidates) > count {
		candidates = candidates[:count]
	}
	return candidates, nil
}

func (f *fakeRepo) GetSeatClassCodes(ctx context.Context, tripID string, seatNos []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, sn := range seatNos {
		s, ok := f.seats[seatKey(tripID, sn)]
		if !ok || seen[s.ClassCode] {
			continue
		}
		seen[s.ClassCode] = true
		out = append(out, s.ClassCode)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeRepo) InsertWaitlistEntry(ctx context.Context, entry WaitlistEntry) (int64, error) {
	f.nextWaitlistID++
	entry.ID = f.nextWaitlistID
	entry.Status = WaitlistPending
	f.waitlist[entry.ID] = entry
	return entry.ID, nil
}

func (f *fakeRepo) GetWaitlistHeadForUpdate(ctx context.Context, tripID, classCode string) (WaitlistEntry, bool, error) {
	var head *WaitlistEntry
	for id, e := range f.waitlist {
		if e.TripID != tripID || e.ClassCode != classCode || e.Status != WaitlistPending {
			continue
		}
		if head == nil || id < head.ID {
			cp := e
			head = &cp
		}
	}
	if head == nil {
		return WaitlistEntry{}, false, nil
	}
	return *head, true, nil
}

func (f *fakeRepo) UpdateWaitlistPromoted(ctx context.Context, id int64, holdID string) error {
	e := f.waitlist[id]
	e.Status = WaitlistPromoted
	e.PromotedHoldID = holdID
	f.waitlist[id] = e
	return nil
}

func (f *fakeRepo) GetWaitlistEntry(ctx context.Context, id int64) (WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.waitlist[id]
	if !ok {
		return WaitlistEntry{}, apperrors.ErrNotFound
	}
	return e, nil
}

func (f *fakeRepo) ListWaitlistEntriesByUser(ctx context.Context, userID string) ([]WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []WaitlistEntry
	for _, e := range f.waitlist {
		if e.UserID == userID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (f *fakeRepo) GetHoldSeatNos(ctx context.Context, holdID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	refs := f.holdSeats[holdID]
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.SeatNo
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeRepo) InsertOutboxEvent(ctx context.Context, tripID, eventType string, payload []byte) error {
	f.nextOutboxID++
	f.outbox[f.nextOutboxID] = OutboxEvent{ID: f.nextOutboxID, TripID: tripID, EventType: eventType, Payload: payload}
	return nil
}

func (f *fakeRepo) ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for id := range f.outbox {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []OutboxEvent
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		out = append(out, f.outbox[id])
	}
	return out, nil
}

func (f *fakeRepo) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.outbox, id) // simplest fake: "published" == removed from the unpublished set
	return nil
}

func availableSeat(tripID, seatNo string) Seat {
	return Seat{TripID: tripID, SeatNo: seatNo, ClassCode: "seater", Status: SeatAvailable}
}

func TestCreateHold(t *testing.T) {
	tests := []struct {
		name    string
		seats   []Seat
		seatNos []string
		wantErr error // nil means "any error", set to a sentinel to check errors.Is
		wantOK  bool
	}{
		{
			name:    "all seats available",
			seats:   []Seat{availableSeat("t1", "1"), availableSeat("t1", "2")},
			seatNos: []string{"1", "2"},
			wantOK:  true,
		},
		{
			name:    "seat already held is a conflict",
			seats:   []Seat{{TripID: "t1", SeatNo: "1", Status: SeatHeld, HoldID: "other-hold"}},
			seatNos: []string{"1"},
			wantErr: apperrors.ErrConflict,
		},
		{
			name:    "seat already booked is a conflict",
			seats:   []Seat{{TripID: "t1", SeatNo: "1", Status: SeatBooked, BookingID: "b1"}},
			seatNos: []string{"1"},
			wantErr: apperrors.ErrConflict,
		},
		{
			name:    "unknown seat is not found",
			seats:   []Seat{availableSeat("t1", "1")},
			seatNos: []string{"1", "does-not-exist"},
			wantErr: apperrors.ErrNotFound,
		},
		{
			name:    "duplicate seat numbers rejected before touching repo",
			seats:   []Seat{availableSeat("t1", "1")},
			seatNos: []string{"1", "1"},
			wantErr: nil, // validation error, no sentinel
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo(tt.seats)
			svc := NewService(repo)

			hold, err := svc.CreateHold(context.Background(), "t1", "user1", tt.seatNos, 8*time.Minute)

			if tt.wantOK {
				if err != nil {
					t.Fatalf("CreateHold() error = %v, want nil", err)
				}
				if hold.ID == "" {
					t.Error("CreateHold() returned empty hold ID")
				}
				for _, sn := range tt.seatNos {
					s := repo.seats[seatKey("t1", sn)]
					if s.Status != SeatHeld || s.HoldID != hold.ID {
						t.Errorf("seat %s not held by returned hold: status=%s holdID=%s", sn, s.Status, s.HoldID)
					}
				}
				return
			}

			if err == nil {
				t.Fatal("CreateHold() error = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("CreateHold() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

// TestCreateHold_ConcurrentSameSeat proves the service never lets two
// concurrent CreateHold calls both succeed for the same seat: with the
// fake repo's WithinTx serializing callers, this exercises the exact
// check-then-act logic that must hold once real transactions serialize
// the same way. Real cross-connection contention is covered by the
// testcontainers integration test, not here.
func TestCreateHold_ConcurrentSameSeat(t *testing.T) {
	const attempts = 100
	repo := newFakeRepo([]Seat{availableSeat("t1", "1")})
	svc := NewService(repo)

	var succeeded, conflicts int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateHold(context.Background(), "t1", "user1", []string{"1"}, 8*time.Minute)
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
		t.Errorf("succeeded = %d, want exactly 1 (no double-booking)", succeeded)
	}
	if conflicts != attempts-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, attempts-1)
	}
}

func TestReleaseHold(t *testing.T) {
	baseSeats := []Seat{{TripID: "t1", SeatNo: "1", Status: SeatHeld, HoldID: "h1"}}

	tests := []struct {
		name       string
		holdStatus HoldStatus
		wantErr    error
	}{
		{"active hold releases", HoldActive, nil},
		{"already released is idempotent", HoldReleased, nil},
		{"already expired is idempotent", HoldExpired, nil},
		{"confirmed hold cannot be released", HoldConfirmed, apperrors.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo(append([]Seat{}, baseSeats...))
			repo.holds["h1"] = Hold{ID: "h1", TripID: "t1", Status: tt.holdStatus, ExpiresAt: time.Now().Add(time.Hour)}
			repo.holdSeats["h1"] = []SeatRef{{TripID: "t1", SeatNo: "1"}}
			svc := NewService(repo)

			err := svc.ReleaseHold(context.Background(), "h1")

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ReleaseHold() error = %v, want nil", err)
				}
				if tt.holdStatus == HoldActive {
					s := repo.seats[seatKey("t1", "1")]
					if s.Status != SeatAvailable || s.HoldID != "" {
						t.Errorf("seat not released: status=%s holdID=%s", s.Status, s.HoldID)
					}
					if repo.holds["h1"].Status != HoldReleased {
						t.Errorf("hold status = %s, want released", repo.holds["h1"].Status)
					}
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ReleaseHold() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestReleaseHold_NotFound(t *testing.T) {
	repo := newFakeRepo(nil)
	svc := NewService(repo)
	err := svc.ReleaseHold(context.Background(), "does-not-exist")
	if !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("ReleaseHold() error = %v, want errors.Is(_, ErrNotFound)", err)
	}
}

func TestConfirmHold(t *testing.T) {
	tests := []struct {
		name       string
		holdStatus HoldStatus
		wantErr    error
	}{
		{"active hold confirms", HoldActive, nil},
		{"already confirmed is idempotent", HoldConfirmed, nil},
		{"released hold cannot be confirmed", HoldReleased, apperrors.ErrConflict},
		{"expired hold cannot be confirmed", HoldExpired, apperrors.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo([]Seat{{TripID: "t1", SeatNo: "1", Status: SeatHeld, HoldID: "h1"}})
			repo.holds["h1"] = Hold{ID: "h1", TripID: "t1", Status: tt.holdStatus}
			repo.holdSeats["h1"] = []SeatRef{{TripID: "t1", SeatNo: "1"}}
			svc := NewService(repo)

			err := svc.ConfirmHold(context.Background(), "h1", "booking-1")

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ConfirmHold() error = %v, want nil", err)
				}
				if tt.holdStatus == HoldActive {
					s := repo.seats[seatKey("t1", "1")]
					if s.Status != SeatBooked || s.BookingID != "booking-1" {
						t.Errorf("seat not booked: status=%s bookingID=%s", s.Status, s.BookingID)
					}
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ConfirmHold() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestCancelBooking(t *testing.T) {
	tests := []struct {
		name      string
		bookingID string
		callWith  string
		wantErr   error
	}{
		{"matching booking releases seat", "b1", "b1", nil},
		{"mismatched booking is a conflict", "b1", "b2", apperrors.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo([]Seat{{TripID: "t1", SeatNo: "1", Status: SeatBooked, BookingID: tt.bookingID}})
			svc := NewService(repo)

			err := svc.CancelBooking(context.Background(), "t1", tt.callWith, []string{"1"})

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("CancelBooking() error = %v, want nil", err)
				}
				s := repo.seats[seatKey("t1", "1")]
				if s.Status != SeatAvailable {
					t.Errorf("seat status = %s, want available", s.Status)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("CancelBooking() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestSweepExpiredHolds(t *testing.T) {
	repo := newFakeRepo([]Seat{
		{TripID: "t1", SeatNo: "1", Status: SeatHeld, HoldID: "expired"},
		{TripID: "t1", SeatNo: "2", Status: SeatHeld, HoldID: "not-expired"},
	})
	repo.holds["expired"] = Hold{ID: "expired", TripID: "t1", Status: HoldActive, ExpiresAt: time.Now().Add(-time.Minute)}
	repo.holdSeats["expired"] = []SeatRef{{TripID: "t1", SeatNo: "1"}}
	repo.holds["not-expired"] = Hold{ID: "not-expired", TripID: "t1", Status: HoldActive, ExpiresAt: time.Now().Add(time.Hour)}
	repo.holdSeats["not-expired"] = []SeatRef{{TripID: "t1", SeatNo: "2"}}

	svc := NewService(repo)
	n, err := svc.SweepExpiredHolds(context.Background(), 10)
	if err != nil {
		t.Fatalf("SweepExpiredHolds() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepExpiredHolds() released %d holds, want 1", n)
	}
	if repo.holds["expired"].Status != HoldReleased {
		t.Errorf("expired hold status = %s, want released", repo.holds["expired"].Status)
	}
	if repo.seats[seatKey("t1", "1")].Status != SeatAvailable {
		t.Errorf("expired hold's seat not released")
	}
	if repo.holds["not-expired"].Status != HoldActive {
		t.Errorf("non-expired hold was touched: status = %s", repo.holds["not-expired"].Status)
	}
	if repo.seats[seatKey("t1", "2")].Status != SeatHeld {
		t.Errorf("non-expired hold's seat was released")
	}
}
