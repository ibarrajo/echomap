package geo

// ReverseGeocode returns the nearest known city name for a lat/lon coordinate.
func ReverseGeocode(lat, lon float64) string {
	bestDist := 999999.0
	bestName := "Unknown"

	for _, c := range knownCities {
		d := HaversineKM(lat, lon, c.lat, c.lon)
		if d < bestDist {
			bestDist = d
			bestName = c.name
		}
	}

	return bestName
}

// RegionLabel returns a human-readable label like "Near Amsterdam, Netherlands (52.37°N, 4.90°E) ± 150 km"
func RegionLabel(lat, lon, radiusKM float64) string {
	city := ReverseGeocode(lat, lon)
	latDir := "N"
	if lat < 0 {
		latDir = "S"
		lat = -lat
	}
	lonDir := "E"
	if lon < 0 {
		lonDir = "W"
		lon = -lon
	}
	return city + " (" + formatFloat(lat, 2) + "°" + latDir + ", " + formatFloat(lon, 2) + "°" + lonDir + ")"
}

func formatFloat(f float64, prec int) string {
	// Simple formatting without importing fmt to keep this lean
	switch prec {
	case 2:
		s := ""
		whole := int(f)
		frac := int((f - float64(whole)) * 100)
		if frac < 0 {
			frac = -frac
		}
		s = intToStr(whole) + "."
		if frac < 10 {
			s += "0"
		}
		s += intToStr(frac)
		return s
	default:
		return intToStr(int(f))
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	s := string(digits)
	if neg {
		s = "-" + s
	}
	return s
}

type cityEntry struct {
	name string
	lat  float64
	lon  float64
}

// Major cities worldwide for reverse geocoding.
var knownCities = []cityEntry{
	// North America
	{"New York, US", 40.7128, -74.0060},
	{"Los Angeles, US", 33.9425, -118.4081},
	{"Chicago, US", 41.8781, -87.6298},
	{"Houston, US", 29.7604, -95.3698},
	{"Phoenix, US", 33.4484, -112.0740},
	{"San Francisco, US", 37.7749, -122.4194},
	{"Seattle, US", 47.6062, -122.3321},
	{"Miami, US", 25.7617, -80.1918},
	{"Atlanta, US", 33.7490, -84.3880},
	{"Dallas, US", 32.7767, -96.7970},
	{"Denver, US", 39.7392, -104.9903},
	{"Washington DC, US", 38.9072, -77.0369},
	{"Boston, US", 42.3601, -71.0589},
	{"Toronto, CA", 43.6532, -79.3832},
	{"Montreal, CA", 45.5017, -73.5673},
	{"Vancouver, CA", 49.2827, -123.1207},
	{"Mexico City, MX", 19.4326, -99.1332},

	// South America
	{"Sao Paulo, BR", -23.5505, -46.6333},
	{"Buenos Aires, AR", -34.6037, -58.3816},
	{"Santiago, CL", -33.4489, -70.6693},
	{"Lima, PE", -12.0464, -77.0428},
	{"Bogota, CO", 4.7110, -74.0721},

	// Europe
	{"London, GB", 51.5074, -0.1278},
	{"Paris, FR", 48.8566, 2.3522},
	{"Amsterdam, NL", 52.3676, 4.9041},
	{"Frankfurt, DE", 50.1109, 8.6821},
	{"Berlin, DE", 52.5200, 13.4050},
	{"Munich, DE", 48.1351, 11.5820},
	{"Madrid, ES", 40.4168, -3.7038},
	{"Barcelona, ES", 41.3874, 2.1686},
	{"Rome, IT", 41.9028, 12.4964},
	{"Milan, IT", 45.4642, 9.1900},
	{"Zurich, CH", 47.3769, 8.5417},
	{"Vienna, AT", 48.2082, 16.3738},
	{"Brussels, BE", 50.8503, 4.3517},
	{"Dublin, IE", 53.3498, -6.2603},
	{"Stockholm, SE", 59.3293, 18.0686},
	{"Oslo, NO", 59.9139, 10.7522},
	{"Copenhagen, DK", 55.6761, 12.5683},
	{"Helsinki, FI", 60.1699, 24.9384},
	{"Warsaw, PL", 52.2297, 21.0122},
	{"Prague, CZ", 50.0755, 14.4378},
	{"Budapest, HU", 47.4979, 19.0402},
	{"Lisbon, PT", 38.7223, -9.1393},
	{"Moscow, RU", 55.7558, 37.6173},
	{"Istanbul, TR", 41.0082, 28.9784},
	{"Athens, GR", 37.9838, 23.7275},
	{"Bucharest, RO", 44.4268, 26.1025},
	{"Kyiv, UA", 50.4501, 30.5234},

	// Asia
	{"Tokyo, JP", 35.6762, 139.6503},
	{"Seoul, KR", 37.5665, 126.9780},
	{"Shanghai, CN", 31.2304, 121.4737},
	{"Beijing, CN", 39.9042, 116.4074},
	{"Hong Kong, HK", 22.3193, 114.1694},
	{"Singapore, SG", 1.3521, 103.8198},
	{"Mumbai, IN", 19.0760, 72.8777},
	{"Delhi, IN", 28.7041, 77.1025},
	{"Bangalore, IN", 12.9716, 77.5946},
	{"Bangkok, TH", 13.7563, 100.5018},
	{"Jakarta, ID", -6.2088, 106.8456},
	{"Taipei, TW", 25.0330, 121.5654},
	{"Dubai, AE", 25.2048, 55.2708},
	{"Tel Aviv, IL", 32.0853, 34.7818},

	// Africa
	{"Johannesburg, ZA", -26.2041, 28.0473},
	{"Cape Town, ZA", -33.9249, 18.4241},
	{"Lagos, NG", 6.5244, 3.3792},
	{"Nairobi, KE", -1.2921, 36.8219},
	{"Cairo, EG", 30.0444, 31.2357},

	// Oceania
	{"Sydney, AU", -33.8688, 151.2093},
	{"Melbourne, AU", -37.8136, 144.9631},
	{"Auckland, NZ", -36.8485, 174.7633},
}
