package analytics

// rates is Meta's per-message price in hundredths of a paisa (₹0.7846 is 7846) for a billable
// message, by recipient country and pricing category. It is an estimate for the dashboard
// only: Meta bills the business directly and changes the rate card from time to time, so keep
// this in step with https://developers.facebook.com/docs/whatsapp/pricing. Service messages
// are free.
var rates = map[string]map[string]int64{
	"IN": {"marketing": 7846, "utility": 1150, "authentication": 1150},
}

// Cost estimates Meta's charge for n billable messages. ok is false when the country has no
// rate here, so the dashboard can say the estimate is partial.
func Cost(country, category string, n int32) (paise int64, ok bool) {
	if category == "service" || n == 0 {
		return 0, true
	}
	r, found := rates[country]
	if !found {
		return 0, false
	}
	p, found := r[category]
	if !found {
		return 0, false
	}
	return (p*int64(n) + 50) / 100, true
}
