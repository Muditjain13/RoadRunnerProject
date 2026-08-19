package geo

import "testing"

func TestDistanceKm(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		dest    string
		want    int
		wantErr bool
	}{
		{"known pair", DEL, BOM, 1400, false},
		{"same city", DEL, DEL, 0, false},
		{"symmetric", BOM, DEL, 1400, false},
		{"unknown origin", "XXX", BOM, 0, true},
		{"unknown dest", DEL, "XXX", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DistanceKm(tt.origin, tt.dest)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DistanceKm(%s, %s) error = %v, wantErr %v", tt.origin, tt.dest, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("DistanceKm(%s, %s) = %d, want %d", tt.origin, tt.dest, got, tt.want)
			}
		})
	}
}

func TestDistanceKm_Symmetric(t *testing.T) {
	for a := range Cities {
		for b := range Cities {
			ab, err := DistanceKm(a, b)
			if err != nil {
				t.Fatalf("DistanceKm(%s, %s) error = %v", a, b, err)
			}
			ba, err := DistanceKm(b, a)
			if err != nil {
				t.Fatalf("DistanceKm(%s, %s) error = %v", b, a, err)
			}
			if ab != ba {
				t.Errorf("distance not symmetric: DistanceKm(%s,%s)=%d, DistanceKm(%s,%s)=%d", a, b, ab, b, a, ba)
			}
		}
	}
}

func TestAllCityPairs(t *testing.T) {
	pairs := AllCityPairs()
	if len(pairs) != 90 {
		t.Fatalf("AllCityPairs() returned %d pairs, want 90", len(pairs))
	}
	seen := make(map[[2]string]bool, len(pairs))
	for _, p := range pairs {
		if p[0] == p[1] {
			t.Errorf("AllCityPairs() included self-pair %v", p)
		}
		if seen[p] {
			t.Errorf("AllCityPairs() duplicate pair %v", p)
		}
		seen[p] = true
	}
}
