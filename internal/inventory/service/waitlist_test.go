package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

func trainSeat(tripID, seatNo string) Seat {
	return Seat{TripID: tripID, SeatNo: seatNo, ClassCode: "sl", Status: SeatAvailable}
}

func TestCreateHoldByClass_HoldsWhenEnoughSeats(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1"), trainSeat("t1", "2"), trainSeat("t1", "3")})
	svc := NewService(repo)

	outcome, err := svc.CreateHoldByClass(context.Background(), "t1", "sl", "user1", 2, 50000, 8*time.Minute)
	if err != nil {
		t.Fatalf("CreateHoldByClass() error = %v", err)
	}
	if outcome.Hold == nil || outcome.Waitlist != nil {
		t.Fatalf("outcome = %+v, want a Hold and no Waitlist", outcome)
	}
	if len(outcome.Hold.SeatNos) != 2 {
		t.Errorf("held %d seats, want 2", len(outcome.Hold.SeatNos))
	}
	for _, sn := range outcome.Hold.SeatNos {
		s := repo.seats[seatKey("t1", sn)]
		if s.Status != SeatHeld || s.HoldID != outcome.Hold.ID {
			t.Errorf("seat %s not held by returned hold: %+v", sn, s)
		}
	}
}

func TestCreateHoldByClass_WaitlistsWhenNotEnoughSeats(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1")}) // only 1 seat, asking for 2
	svc := NewService(repo)

	outcome, err := svc.CreateHoldByClass(context.Background(), "t1", "sl", "user1", 2, 50000, 8*time.Minute)
	if err != nil {
		t.Fatalf("CreateHoldByClass() error = %v", err)
	}
	if outcome.Waitlist == nil || outcome.Hold != nil {
		t.Fatalf("outcome = %+v, want a Waitlist entry and no Hold", outcome)
	}
	if outcome.Waitlist.Status != WaitlistPending {
		t.Errorf("waitlist status = %s, want pending", outcome.Waitlist.Status)
	}
	if outcome.Waitlist.SeatCount != 2 || outcome.Waitlist.AmountPaise != 50000 {
		t.Errorf("waitlist entry = %+v, want seatCount=2 amountPaise=50000", outcome.Waitlist)
	}
	// No partial hold: the one available seat must still be available, not
	// held - CreateHoldByClass either holds all seatCount seats or none.
	s := repo.seats[seatKey("t1", "1")]
	if s.Status != SeatAvailable {
		t.Errorf("seat 1 status = %s, want available (no partial hold)", s.Status)
	}
}

func TestPromoteWaitlistHead_PromotesFIFO(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1"), trainSeat("t1", "2")})
	svc := NewService(repo)

	// Two entries queued for the same (trip, class); only enough seats
	// free for one 2-seat request at a time.
	older, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u-older", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u-newer", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}

	promoted, err := svc.PromoteWaitlistHead(context.Background(), "t1", "sl")
	if err != nil {
		t.Fatalf("PromoteWaitlistHead() error = %v", err)
	}
	if !promoted {
		t.Fatal("PromoteWaitlistHead() = false, want true (2 seats free, 2 requested)")
	}

	olderEntry := repo.waitlist[older]
	newerEntry := repo.waitlist[newer]
	if olderEntry.Status != WaitlistPromoted {
		t.Errorf("older entry status = %s, want promoted (FIFO: oldest should go first)", olderEntry.Status)
	}
	if newerEntry.Status != WaitlistPending {
		t.Errorf("newer entry status = %s, want still pending (no seats left for it)", newerEntry.Status)
	}
	if olderEntry.PromotedHoldID == "" {
		t.Fatal("older entry has no promoted hold id")
	}
	hold, ok := repo.holds[olderEntry.PromotedHoldID]
	if !ok {
		t.Fatal("promoted hold not found in repo")
	}
	if hold.UserID != "u-older" || hold.Status != HoldActive {
		t.Errorf("promoted hold = %+v, want active hold for u-older", hold)
	}
}

func TestPromoteWaitlistHead_NoopWhenNotEnoughSeats(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1")}) // 1 seat free, entry wants 2
	svc := NewService(repo)
	id, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u1", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}

	promoted, err := svc.PromoteWaitlistHead(context.Background(), "t1", "sl")
	if err != nil {
		t.Fatalf("PromoteWaitlistHead() error = %v", err)
	}
	if promoted {
		t.Fatal("PromoteWaitlistHead() = true, want false (not enough seats)")
	}
	if repo.waitlist[id].Status != WaitlistPending {
		t.Errorf("entry status = %s, want still pending", repo.waitlist[id].Status)
	}
}

func TestPromoteWaitlistHead_NoopWhenNoPendingEntry(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1")})
	svc := NewService(repo)

	promoted, err := svc.PromoteWaitlistHead(context.Background(), "t1", "sl")
	if err != nil {
		t.Fatalf("PromoteWaitlistHead() error = %v", err)
	}
	if promoted {
		t.Fatal("PromoteWaitlistHead() = true, want false (no pending entries)")
	}
}

// TestPromoteWaitlistHead_NeverDoublePromotes proves the core correctness
// property of the waitlist under concurrency: with two pending entries
// each wanting 2 seats, and only 2 seats ever free, many concurrent
// promotion attempts (simulating racing seats-freed events, e.g. two
// cancellations landing at once) must promote exactly one entry - never
// zero, never both, and never the same entry twice.
func TestPromoteWaitlistHead_NeverDoublePromotes(t *testing.T) {
	const attempts = 50
	repo := newFakeRepo([]Seat{trainSeat("t1", "1"), trainSeat("t1", "2")})
	svc := NewService(repo)

	e1, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u1", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u2", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}

	var promotions int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			promoted, err := svc.PromoteWaitlistHead(context.Background(), "t1", "sl")
			if err != nil {
				t.Errorf("PromoteWaitlistHead() error = %v", err)
				return
			}
			if promoted {
				atomic.AddInt64(&promotions, 1)
			}
		}()
	}
	wg.Wait()

	if promotions != 1 {
		t.Errorf("promotions = %d, want exactly 1 (only 2 seats ever existed, 2 entries wanted 2 each)", promotions)
	}
	promotedCount := 0
	if repo.waitlist[e1].Status == WaitlistPromoted {
		promotedCount++
	}
	if repo.waitlist[e2].Status == WaitlistPromoted {
		promotedCount++
	}
	if promotedCount != 1 {
		t.Errorf("promoted entries = %d, want exactly 1", promotedCount)
	}
	// Whichever entry promoted, both its seats must be held under its own
	// hold - no seat can end up double-assigned across two holds.
	seenSeats := make(map[string]string)
	for _, h := range repo.holds {
		if h.Status != HoldActive {
			continue
		}
		for _, sn := range h.SeatNos {
			if owner, ok := seenSeats[sn]; ok {
				t.Errorf("seat %s held by both %s and %s", sn, owner, h.ID)
			}
			seenSeats[sn] = h.ID
		}
	}
}

func TestClaimPromotedWaitlist(t *testing.T) {
	repo := newFakeRepo([]Seat{trainSeat("t1", "1"), trainSeat("t1", "2")})
	svc := NewService(repo)
	id, err := repo.InsertWaitlistEntry(context.Background(), WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u1", SeatCount: 2, AmountPaise: 1000})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.ClaimPromotedWaitlist(context.Background(), id, "u1"); !errors.Is(err, apperrors.ErrConflict) {
		t.Errorf("claim before promotion: error = %v, want ErrConflict", err)
	}

	if _, err := svc.PromoteWaitlistHead(context.Background(), "t1", "sl"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.ClaimPromotedWaitlist(context.Background(), id, "someone-else"); !errors.Is(err, apperrors.ErrConflict) {
		t.Errorf("claim by wrong user: error = %v, want ErrConflict", err)
	}

	holdID, seatNos, err := svc.ClaimPromotedWaitlist(context.Background(), id, "u1")
	if err != nil {
		t.Fatalf("ClaimPromotedWaitlist() error = %v", err)
	}
	if holdID == "" || len(seatNos) != 2 {
		t.Errorf("claim result = holdID=%q seatNos=%v, want a hold with 2 seats", holdID, seatNos)
	}

	// Idempotent: claiming again returns the same result.
	holdID2, seatNos2, err := svc.ClaimPromotedWaitlist(context.Background(), id, "u1")
	if err != nil || holdID2 != holdID || len(seatNos2) != len(seatNos) {
		t.Errorf("second claim = (%q, %v, %v), want same as first (%q, %v)", holdID2, seatNos2, err, holdID, seatNos)
	}
}

func TestListWaitlistByUser(t *testing.T) {
	repo := newFakeRepo(nil)
	svc := NewService(repo)
	ctx := context.Background()
	if _, err := repo.InsertWaitlistEntry(ctx, WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u1", SeatCount: 1, AmountPaise: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertWaitlistEntry(ctx, WaitlistEntry{TripID: "t2", ClassCode: "3a", UserID: "u1", SeatCount: 1, AmountPaise: 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertWaitlistEntry(ctx, WaitlistEntry{TripID: "t1", ClassCode: "sl", UserID: "u2", SeatCount: 1, AmountPaise: 100}); err != nil {
		t.Fatal(err)
	}

	entries, err := svc.ListWaitlistByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListWaitlistByUser() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if e.UserID != "u1" {
			t.Errorf("entry for wrong user: %+v", e)
		}
	}
}
