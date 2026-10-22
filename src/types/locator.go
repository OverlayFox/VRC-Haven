package types

import (
	"fmt"
	"net"

	pgeo "github.com/paulmach/go.geo"
)

// Location represents the geographical location of a connection, used for selecting the closest escort.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`

	CountryName string `json:"country"`
	StateName   string `json:"state"`
	City        string `json:"city"`
}

func (l Location) GetGeoPoint() *pgeo.Point {
	return pgeo.NewPoint(l.Longitude, l.Latitude)
}

func (l Location) GetDistanceBetween(p2 *pgeo.Point) float64 {
	return l.GetGeoPoint().DistanceFrom(p2)
}

func (l Location) String() string {
	return fmt.Sprintf("%s, %s, %s (%.4f, %.4f)", l.City, l.StateName, l.CountryName, l.Latitude, l.Longitude)
}

func (l Location) IsSet() bool {
	return l.Latitude != 0 || l.Longitude != 0
}

type Locator interface {
	GetLocation(addr net.Addr) (Location, error)
}
