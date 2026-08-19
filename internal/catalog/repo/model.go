package repo

import "time"

// operatorDoc is the "operators" collection.
type operatorDoc struct {
	ID   string `bson:"_id"`
	Name string `bson:"name"`
	Mode string `bson:"mode"`
}

// routeDoc is the "routes" collection - authoritative route metadata.
type routeDoc struct {
	ID         string `bson:"_id"`
	OriginCode string `bson:"origin_code"`
	DestCode   string `bson:"dest_code"`
	DistanceKm int    `bson:"distance_km"`
}

// tripClassDoc is one bookable class embedded in a tripDoc.
type tripClassDoc struct {
	Code      string `bson:"code"`
	FarePaise int64  `bson:"fare_paise"`
	Capacity  int    `bson:"capacity"`
}

// tripDoc is the "trips" collection. origin_code/dest_code/distance_km are
// denormalized from the route so search is a single query with no join -
// Mongo has no efficient join, and search-by-route-and-date is the hot
// path this collection exists to serve. route_id is kept for lookups that
// need full route metadata.
type tripDoc struct {
	ID            string         `bson:"_id"`
	RouteID       string         `bson:"route_id"`
	OperatorID    string         `bson:"operator_id"`
	OperatorName  string         `bson:"operator_name"`
	Mode          string         `bson:"mode"`
	OriginCode    string         `bson:"origin_code"`
	DestCode      string         `bson:"dest_code"`
	DistanceKm    int            `bson:"distance_km"`
	VehicleNumber string         `bson:"vehicle_number"`
	DepartureAt   time.Time      `bson:"departure_at"`
	ArrivalAt     time.Time      `bson:"arrival_at"`
	Classes       []tripClassDoc `bson:"classes"`
}
