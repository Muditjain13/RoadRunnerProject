// Package geo holds the fixed seed reference data for cities and the
// road-distance matrix between them (see CLAUDE.md section 9). It is the
// single source of truth shared by the seeder (pricing, journey duration)
// and the catalog service (resolving city codes for search results), so it
// lives in pkg rather than being duplicated per service.
package geo

import "fmt"

// City is one of the ten fixed cities in the seed dataset.
type City struct {
	Code  string
	Name  string
	State string
	Lat   float64
	Lon   float64
}

// Codes, in the fixed order used to index the distance matrix.
const (
	DEL = "DEL"
	BOM = "BOM"
	MAA = "MAA"
	BLR = "BLR"
	PNQ = "PNQ"
	JAI = "JAI"
	GAU = "GAU"
	SXR = "SXR"
	AMD = "AMD"
	IDR = "IDR"
)

// cityOrder fixes the row/column order of distanceKm below.
var cityOrder = []string{DEL, BOM, MAA, BLR, PNQ, JAI, GAU, SXR, AMD, IDR}

var Cities = map[string]City{
	DEL: {Code: DEL, Name: "Delhi", State: "Delhi", Lat: 28.61, Lon: 77.21},
	BOM: {Code: BOM, Name: "Mumbai", State: "Maharashtra", Lat: 19.08, Lon: 72.88},
	MAA: {Code: MAA, Name: "Chennai", State: "Tamil Nadu", Lat: 13.08, Lon: 80.27},
	BLR: {Code: BLR, Name: "Bengaluru", State: "Karnataka", Lat: 12.97, Lon: 77.59},
	PNQ: {Code: PNQ, Name: "Pune", State: "Maharashtra", Lat: 18.52, Lon: 73.86},
	JAI: {Code: JAI, Name: "Jaipur", State: "Rajasthan", Lat: 26.91, Lon: 75.79},
	GAU: {Code: GAU, Name: "Guwahati", State: "Assam", Lat: 26.14, Lon: 91.74},
	SXR: {Code: SXR, Name: "Srinagar", State: "Jammu and Kashmir", Lat: 34.08, Lon: 74.80},
	AMD: {Code: AMD, Name: "Ahmedabad", State: "Gujarat", Lat: 23.02, Lon: 72.57},
	IDR: {Code: IDR, Name: "Indore", State: "Madhya Pradesh", Lat: 22.72, Lon: 75.86},
}

// distanceKm[i][j] is the approximate road distance in km between
// cityOrder[i] and cityOrder[j]. Symmetric, zero diagonal.
var distanceKm = [10][10]int{
	/*        DEL   BOM   MAA   BLR   PNQ   JAI   GAU   SXR   AMD   IDR */
	/* DEL */ {0, 1400, 2180, 2150, 1420, 280, 1870, 810, 940, 810},
	/* BOM */ {1400, 0, 1330, 980, 150, 1170, 2650, 2200, 530, 590},
	/* MAA */ {2180, 1330, 0, 350, 1180, 2100, 2900, 3000, 1850, 1550},
	/* BLR */ {2150, 980, 350, 0, 840, 1980, 2900, 2960, 1490, 1370},
	/* PNQ */ {1420, 150, 1180, 840, 0, 1170, 2500, 2150, 660, 590},
	/* JAI */ {280, 1170, 2100, 1980, 1170, 0, 1900, 1000, 660, 590},
	/* GAU */ {1870, 2650, 2900, 2900, 2500, 1900, 0, 2500, 2600, 2100},
	/* SXR */ {810, 2200, 3000, 2960, 2150, 1000, 2500, 0, 1600, 1500},
	/* AMD */ {940, 530, 1850, 1490, 660, 660, 2600, 1600, 0, 400},
	/* IDR */ {810, 590, 1550, 1370, 590, 590, 2100, 1500, 400, 0},
}

var cityIndex = func() map[string]int {
	m := make(map[string]int, len(cityOrder))
	for i, c := range cityOrder {
		m[c] = i
	}
	return m
}()

// DistanceKm returns the road distance between two city codes. Returns an
// error if either code is not one of the ten fixed cities.
func DistanceKm(origin, dest string) (int, error) {
	i, ok := cityIndex[origin]
	if !ok {
		return 0, fmt.Errorf("geo: unknown city code %q", origin)
	}
	j, ok := cityIndex[dest]
	if !ok {
		return 0, fmt.Errorf("geo: unknown city code %q", dest)
	}
	return distanceKm[i][j], nil
}

// AllCityPairs returns all 90 directed (origin, dest) pairs, origin != dest.
func AllCityPairs() [][2]string {
	pairs := make([][2]string, 0, len(cityOrder)*(len(cityOrder)-1))
	for _, o := range cityOrder {
		for _, d := range cityOrder {
			if o != d {
				pairs = append(pairs, [2]string{o, d})
			}
		}
	}
	return pairs
}
