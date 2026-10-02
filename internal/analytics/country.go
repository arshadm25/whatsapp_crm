package analytics

// callingCodes maps international calling codes to ISO 3166-1 alpha-2 countries for the markets
// our customers message most. Numbers with other codes are reported as "other".
var callingCodes = map[string]string{
	"91": "IN", "1": "US", "44": "GB", "971": "AE", "966": "SA", "974": "QA", "965": "KW", "968": "OM",
	"973": "BH", "65": "SG", "60": "MY", "62": "ID", "63": "PH", "66": "TH", "84": "VN", "880": "BD",
	"977": "NP", "94": "LK", "92": "PK", "960": "MV", "975": "BT", "61": "AU", "64": "NZ", "49": "DE",
	"33": "FR", "39": "IT", "34": "ES", "31": "NL", "353": "IE", "27": "ZA", "234": "NG", "254": "KE",
	"20": "EG", "55": "BR", "52": "MX", "7": "RU", "86": "CN", "81": "JP", "82": "KR", "852": "HK",
}

// Country returns the ISO country of a WhatsApp number (digits, no "+") from its calling code.
// The longest matching code wins, so 971 is the UAE rather than country code 9.
func Country(waID string) string {
	for n := 3; n >= 1; n-- {
		if len(waID) >= n {
			if c, ok := callingCodes[waID[:n]]; ok {
				return c
			}
		}
	}
	return "other"
}
